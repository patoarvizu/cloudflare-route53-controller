package main

import (
	"fmt"
	"os"
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
	kubeclientset      kubernetes.Interface
	workqueue          workqueue.RateLimitingInterface
	recorder           record.EventRecorder
	ingressLister      ingresslisters.IngressLister
	ingressSynced      cache.InformerSynced
	annotationPrefix   string
	hostedZoneId       string
	cloudflareZoneName string
}

func NewController(
	kubeclientset kubernetes.Interface,
	ingressInformer ingressinformers.IngressInformer,
	annotationPrefix string,
	hostedZoneId string,
	cloudflareZoneName string) *Controller {

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})
	controller := &Controller{
		kubeclientset:      kubeclientset,
		workqueue:          workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Queue"),
		recorder:           recorder,
		ingressLister:      ingressInformer.Lister(),
		ingressSynced:      ingressInformer.Informer().HasSynced,
		annotationPrefix:   annotationPrefix,
		hostedZoneId:       hostedZoneId,
		cloudflareZoneName: cloudflareZoneName,
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

	if v, ok := ingress.Annotations[fmt.Sprintf("%s/cloudflare-record", annotationPrefix)]; ok {
		klog.Info(v)
		if d, ok := ingress.Annotations["dns.alpha.kubernetes.io/external"]; ok {
			if v == d {
				klog.Info(fmt.Sprintf("Origin and Cloudflare record are the same (%s), skipping.", v))
			}

			cf, err := cloudflare.New(os.Getenv("CLOUDFLARE_TOKEN"), os.Getenv("CLOUDFLARE_EMAIL"))
			if err != nil {
				return fmt.Errorf("Error: %v", err)
			}
			zoneId, err := cf.ZoneIDByName(cloudflareZoneName)
			if err != nil {
				return fmt.Errorf("Error: %v", err)
			}

			awsSession := session.Must(session.NewSession())
			r53 := route53.New(awsSession)
			r53.ChangeResourceRecordSets(&route53.ChangeResourceRecordSetsInput{
				HostedZoneId: aws.String(hostedZoneId),
				ChangeBatch: &route53.ChangeBatch{
					Changes: []*route53.Change{
						&route53.Change{
							Action: aws.String(route53.ChangeActionUpsert),
							ResourceRecordSet: &route53.ResourceRecordSet{
								Name: aws.String(v),
								ResourceRecords: []*route53.ResourceRecord{
									&route53.ResourceRecord{
										Value: aws.String(fmt.Sprintf("%s.cdn.cloudflare.net", v)),
									},
								},
								TTL:  aws.Int64(60),
								Type: aws.String(route53.RRTypeCname),
							},
						},
					},
				}})
			cf.CreateDNSRecord(zoneId, cloudflare.DNSRecord{Type: "CNAME", Name: v, Content: d, Proxied: true, TTL: 1})
			c.recorder.Event(ingress, corev1.EventTypeNormal, "Synced", "Cloudflare and Route53 records have been synced.")
		}
	}

	return nil
}

func (c *Controller) enqueueIngress(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.Info("Queued ingress ", key)
	c.workqueue.AddRateLimited(key)
}
