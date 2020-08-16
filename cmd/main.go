package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

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

type id string

func (i id) podName() string {
	return strings.Split(string(i), "/")[0]
}

func (i id) port() string {
	return strings.Split(string(i), ":")[1]
}

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
			stopChannel := make(chan struct{})
			defer close(stopChannel)
			defer runtime.HandleCrash()

			lock := sync.Mutex{}
			forwards := make(map[id]*portforward.PortForwarder)

			startPortForward := func(id id) error {
				url := restClient.Post().Resource("pods").Namespace(namespace).Name(id.podName()).SubResource("portforward").URL()
				transport, upgrader, err := spdy.RoundTripperFor(restConfig)
				if err != nil {
					return fmt.Errorf("failed to create SPDY round tripper: %w", err)
				}
				dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
				forwarder, err := portforward.New(dialer, []string{id.port()}, stopChannel, make(chan struct{}), os.Stdin, os.Stderr)
				if err != nil {
					return fmt.Errorf("failed to create port forwarder: %v", err)
				}
				lock.Lock()
				for i, f := range forwards {
					if i.port() == id.port() {
						fmt.Printf("closing existing port-forward %v\n", i)
						f.Close()
					}
				}
				forwards[id] = forwarder
				lock.Unlock()
				defer func() {
					lock.Lock()
					defer lock.Unlock()
					if forwards[id] == forwarder {
						delete(forwards, id)
					}
				}()
				fmt.Printf("%v on http://localhost:%v\n", id.podName(), id.port())
				err = forwarder.ForwardPorts()
				if err != nil {
					return fmt.Errorf("failed to forward port: %v", err)
				}
				return nil
			}

			podAdded := func(obj interface{}) {
				pod := obj.(*corev1.Pod)
				if pod.Status.Phase != corev1.PodRunning {
					return
				}
				for _, c := range pod.Spec.Containers {
					for _, p := range c.Ports {
						go func(p corev1.ContainerPort) {
							err := startPortForward(id(fmt.Sprintf("%s/%s:%v", pod.Name, c.Name, p.ContainerPort)))
							if err != nil {
								fmt.Printf("failed to start port-forward: %v\n", err)
							}
						}(p)
					}
				}
			}

			podDeleted := func(podName string) {
				lock.Lock()
				defer lock.Unlock()
				var ids []id
				for i, f := range forwards {
					if i.podName() == podName {
						f.Close()
						delete(forwards, i)
						ids = append(ids, i)
					}
				}
				for _, j := range ids {
					for i := range forwards {
						if i.port() == j.port() {
							go func() {
								err := startPortForward(i)
								if err != nil {
									fmt.Printf("failed to start port-forward: %v\n", err)
								}
							}()
							break
						}
					}
				}
			}

			// pods
			podInformer := factory.Core().V1().Pods().Informer()
			podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: podAdded,
				UpdateFunc: func(_, obj interface{}) {
					pod, ok := obj.(*corev1.Pod)
					if ok && pod.GetDeletionTimestamp() == nil {
						podAdded(pod)
					} else {
						podDeleted(pod.Name)
					}
				},
				DeleteFunc: func(obj interface{}) {
					key, _ := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
					_, name, _ := cache.SplitMetaNamespaceKey(key)
					podDeleted(name)
				},
			})
			go podInformer.Run(stopChannel)
			<-stopChannel
		},
	}
	clientcmd.BindOverrideFlags(overrides, cmd.PersistentFlags(), clientcmd.RecommendedConfigOverrideFlags(""))
	err := cmd.Execute()
	if err != nil {
		panic(err)
	}
}
