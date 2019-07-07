package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/klog"
)

var (
	masterURL                       string
	kubeconfig                      string
	annotationPrefix                string
	hostedZoneId                    string
	cloudflareZoneName              string
	enableAdditionalHostsAnnotation bool
	frequencyInSeconds              int
)

func setupSignalHandler() (stopCh <-chan struct{}) {
	stop := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		close(stop)
		<-c
		os.Exit(1)
	}()

	return stop
}

func main() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()
	stopCh := setupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*time.Duration(frequencyInSeconds))

	controller := NewController(
		kubeClient,
		kubeInformerFactory.Extensions().V1beta1().Ingresses(),
		annotationPrefix,
		hostedZoneId,
		cloudflareZoneName,
		enableAdditionalHostsAnnotation,
	)

	kubeInformerFactory.Start(stopCh)

	if err = controller.Run(1, stopCh); err != nil {
		klog.Fatalf("Error running vault controller: %s", err.Error())
	}
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig file.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig.")
	flag.StringVar(&annotationPrefix, "annotation-prefix", "cloudflare.patoarvizu.dev", "The prefix to be used for discovery of managed ingresses.")
	flag.StringVar(&hostedZoneId, "hosted-zone-id", "", "The id of the Route53 hosted zone to be managed.")
	flag.StringVar(&cloudflareZoneName, "cloudflare-zone-name", "", "The name of the Cloudflare zone to be managed.")
	flag.BoolVar(&enableAdditionalHostsAnnotation, "enable-additional-hosts-annotations", false, "Enable flag that allows creating additional records for heach 'Host' in the ingress rules.")
	flag.IntVar(&frequencyInSeconds, "frequency", 30, "The frequency at which the controller runs, in seconds.")
}
