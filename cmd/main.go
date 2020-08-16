package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

func main() {
	overrides := &clientcmd.ConfigOverrides{}
	cmd := cobra.Command{
		Use: "auto-forward",
		Run: func(cmd *cobra.Command, args []string) {
			loader := clientcmd.NewDefaultClientConfigLoadingRules()
			loader.DefaultClientConfig = &clientcmd.DefaultClientConfig
			clientConfig := clientcmd.NewInteractiveDeferredLoadingClientConfig(loader, overrides, os.Stdin)
			namespace, _, err := clientConfig.Namespace()
			if err != nil {
				log.Fatalf("failed to create client config: %v\n", err)
			}
			restConfig, err := clientConfig.ClientConfig()
			if err != nil {
				log.Fatalf("failed to create REST config: %v\n", err)
			}
			restConfig.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
			restConfig.APIPath = "api/v1"
			restConfig.GroupVersion = &schema.GroupVersion{}
			restClient, err := rest.RESTClientFor(restConfig)
			if err != nil {
				log.Fatalf("failed to create REST client: %v\n", err)
			}
			clientset, err := kubernetes.NewForConfig(restConfig)
			if err != nil {
				log.Fatalf("failed to create Kubernetes clientset: %v\n", err)
			}
			factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithNamespace(namespace))
			informer := factory.Core().V1().Pods().Informer()
			stopChannel := make(chan struct{})
			defer close(stopChannel)
			defer runtime.HandleCrash()
			startPortForward := func(url *url.URL, ports []string) error {
				transport, upgrader, err := spdy.RoundTripperFor(restConfig)
				if err != nil {
					return fmt.Errorf("failed to create SPDY round tripper: %w", err)
				}
				dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
				forwarder, err := portforward.New(dialer, ports, stopChannel, make(chan struct{}), os.Stdin, os.Stderr)
				if err != nil {
					return fmt.Errorf("failed to create port forwarder: %v", err)
				}
				for _, port := range ports {
					log.Printf("forwarding http://localhost:%s", port)
				}
				err = forwarder.ForwardPorts()
				if err != nil {
					return fmt.Errorf("failed to forward port: %v", err)
				}
				return nil
			}
			startPodPortForward := func(obj interface{}) {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					return
				}
				log.Printf("detected pod/%s (%s)", pod.Name, pod.Status.Phase)
				if pod.Status.Phase != corev1.PodRunning {
					return
				}
				for _, c := range pod.Spec.Containers {
					for _, p := range c.Ports {
						req := restClient.Post().Resource("pods").Namespace(namespace).Name(pod.Name).SubResource("portforward")
						go func(p corev1.ContainerPort) {
							log.Printf("starting port-forward to %s/%s/%s:%v...\n", "pod", pod.Name, c.Name, p.ContainerPort)
							err := startPortForward(req.URL(), []string{strconv.Itoa(int(p.ContainerPort))})
							if err != nil {
								log.Printf("failed to start port-forward: %v\n", err)
							}
						}(p)
					}
				}
			}
			informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: startPodPortForward,
				UpdateFunc: func(_, obj interface{}) {
					startPodPortForward(obj)
				},
			})
			go informer.Run(stopChannel)
			<-stopChannel
		},
	}
	clientcmd.BindOverrideFlags(overrides, cmd.PersistentFlags(), clientcmd.RecommendedConfigOverrideFlags(""))
	err := cmd.Execute()
	if err != nil {
		panic(err)
	}
}
