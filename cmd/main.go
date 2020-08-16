package main

import (
	"context"
	"log"
	"os"

	"github.com/spf13/cobra"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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
				log.Fatal(err)
			}
			restConfig, err := clientConfig.ClientConfig()
			if err != nil {
				log.Fatal(err)
			}
			clientset, err := kubernetes.NewForConfig(restConfig)
			if err != nil {
				log.Fatal(err)
			}
			_, err = clientset.CoreV1().Pods(namespace).List(context.Background(), v1.ListOptions{})
			if err != nil {
				log.Fatal(err)
			}
		},
	}
	clientcmd.BindOverrideFlags(overrides, cmd.PersistentFlags(), clientcmd.RecommendedConfigOverrideFlags(""))
	err := cmd.Execute()
	if err != nil {
		panic(err)
	}
}
