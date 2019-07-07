package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ingressinformers "k8s.io/client-go/informers/extensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ingresslisters "k8s.io/client-go/listers/extensions/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"github.com/cloudflare/cloudflare-go"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
)

const controllerAgentName = "cloudflare-route53-controller"

type Controller struct {
	kubeclientset                   kubernetes.Interface
	workqueue                       workqueue.RateLimitingInterface
	recorder                        record.EventRecorder
	ingressLister                   ingresslisters.IngressLister
	ingressSynced                   cache.InformerSynced
	annotationPrefix                string
	hostedZoneId                    string
	cloudflareZoneName              string
	enableAdditionalHostsAnnotation bool
}

func NewController(
	kubeclientset kubernetes.Interface,
	ingressInformer ingressinformers.IngressInformer,
	annotationPrefix string,
	hostedZoneId string,
	cloudflareZoneName string,
	enableAdditionalHostsAnnotation bool) *Controller {

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})
	controller := &Controller{
		kubeclientset:                   kubeclientset,
		workqueue:                       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Queue"),
		recorder:                        recorder,
		ingressLister:                   ingressInformer.Lister(),
		ingressSynced:                   ingressInformer.Informer().HasSynced,
		annotationPrefix:                annotationPrefix,
		hostedZoneId:                    hostedZoneId,
		cloudflareZoneName:              cloudflareZoneName,
		enableAdditionalHostsAnnotation: enableAdditionalHostsAnnotation,
	}

	ingressInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueIngress,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueIngress(new)
		},
	})
	return controller
}

func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer c.workqueue.ShutDown()

	klog.Info("Starting controller")

	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.ingressSynced); !ok {
		return fmt.Errorf("Failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second*30, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.workqueue.Done(obj)
		return c.processIngress(obj)
	}(obj)
	if err != nil {
		klog.Info("Error: ", err)
	}
	return true
}

func (c *Controller) processIngress(obj interface{}) error {
	key, _ := obj.(string)
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("Error: %v", err)
	}

	ingress, err := c.ingressLister.Ingresses(namespace).Get(name)
	if err != nil {
		return fmt.Errorf("Error: %v", err)
	}

	if cfr, ok := ingress.Annotations[fmt.Sprintf("%s/cloudflare-record", annotationPrefix)]; ok {
		if d, ok := ingress.Annotations["dns.alpha.kubernetes.io/external"]; ok {
			if cfr == d {
				klog.Info(fmt.Sprintf("Origin and Cloudflare record are the same (%s), skipping.", cfr))
				return nil
			}

			cf, err := cloudflare.New(os.Getenv("CLOUDFLARE_TOKEN"), os.Getenv("CLOUDFLARE_EMAIL"))
			if err != nil {
				return fmt.Errorf("Error: %v", err)
			}
			zoneId, err := cf.ZoneIDByName(cloudflareZoneName)
			if err != nil {
				return fmt.Errorf("Error: %v", err)
			}

			records := []string{cfr}

			addHostsAnnotation := false
			if h, ok := ingress.Annotations[fmt.Sprintf("%s/add-rules-hosts", annotationPrefix)]; ok {
				addHostsAnnotation, _ = strconv.ParseBool(h)
			}

			addAliasesAnnotation := false
			if a, ok := ingress.Annotations[fmt.Sprintf("%s/add-aliases", annotationPrefix)]; ok {
				addAliasesAnnotation, _ = strconv.ParseBool(a)
			}

			aliases := []string{}
			if aliasesAnnotation, ok := ingress.Annotations["ingress.kubernetes.io/server-alias"]; ok {
				aliases = strings.Fields(aliasesAnnotation)
			}

			if enableAdditionalHostsAnnotation && addHostsAnnotation {
				for _, rules := range ingress.Spec.Rules {
					records = append(records, rules.Host)
				}
			}

			if enableAdditionalHostsAnnotation && addAliasesAnnotation {
				for _, alias := range aliases {
					records = append(records, alias)
				}
			}

			awsSession := session.Must(session.NewSession())
			r53 := route53.New(awsSession)
			withErrors := false
			uniqueHosts := removeDuplicates(records)
			for _, record := range uniqueHosts {
				//We do one Route 53 record at a time to minimize the time between corresponding R53 and Cloudflare changes
				_, err = r53.ChangeResourceRecordSets(&route53.ChangeResourceRecordSetsInput{
					HostedZoneId: aws.String(hostedZoneId),
					ChangeBatch: &route53.ChangeBatch{
						Changes: []*route53.Change{
							&route53.Change{
								Action: aws.String(route53.ChangeActionUpsert),
								ResourceRecordSet: &route53.ResourceRecordSet{
									Name: aws.String(record),
									ResourceRecords: []*route53.ResourceRecord{
										&route53.ResourceRecord{
											Value: aws.String(fmt.Sprintf("%s.cdn.cloudflare.net", record)),
										},
									},
									TTL:  aws.Int64(60),
									Type: aws.String(route53.RRTypeCname),
								},
							},
						},
					},
				})
				if err != nil {
					klog.Errorf("Error: %v", err)
					c.recorder.Event(ingress, corev1.EventTypeWarning, "Error", fmt.Sprintf("Route53 record (%s) failed to update.", record))
					withErrors = true
				}

				r, err := cf.DNSRecords(zoneId, cloudflare.DNSRecord{Name: record, ZoneID: zoneId, Type: "CNAME"})
				if err != nil {
					klog.Errorf("Error: %v", err)
				}
				if r != nil && len(r) != 0 { // Cloudflare's API doesn't have an 'upsert' operation, so we do this hack.
					err = cf.UpdateDNSRecord(zoneId, r[0].ID, cloudflare.DNSRecord{Type: "CNAME", Name: record, Content: d, Proxied: true, TTL: 1})
				} else {
					_, err = cf.CreateDNSRecord(zoneId, cloudflare.DNSRecord{Type: "CNAME", Name: record, Content: d, Proxied: true, TTL: 1})
				}
				if err != nil {
					klog.Errorf("Error: %v", err)
					c.recorder.Event(ingress, corev1.EventTypeWarning, "Error", fmt.Sprintf("Cloudflare record (%s) failed to update.", record))
					withErrors = true
				}
			}

			if !withErrors {
				c.recorder.Event(ingress, corev1.EventTypeNormal, "Synced", fmt.Sprintf("Cloudflare and Route53 records for ingress %s have been synced.", key))
			}
		}
	}

	return nil
}

func removeDuplicates(hosts []string) []string {
	found := map[string]bool{}
	result := []string{}
	for _, h := range hosts {
		if !found[h] {
			found[h] = true
			result = append(result, h)
		}
	}
	return result
}

func (c *Controller) enqueueIngress(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}
