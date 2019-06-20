package utils

import (
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"

	"k8s.io/client-go/rest"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var clientSet *kubernetes.Clientset
var kubeConfig *rest.Config

func initKubeConfig() {
	kubeConfig = GetConfig()
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		panic(err)
	}

	clientSet = clientset
}

func GetConfig() *rest.Config {
	if kubeConfig == nil {
		var kubeconfig *string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()

		conf, err := rest.InClusterConfig()
		if err != nil {
			fmt.Println("ERR - " + err.Error())
			conf, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
			if err != nil {
				panic(err)
			}
		}

		return conf
	}

	return kubeConfig
}

func GetKubeClient() *kubernetes.Clientset {
	if clientSet == nil {
		initKubeConfig()
	}

	return clientSet
}

func Int32Ptr(i int32) *int32 { return &i }

func RunCMD(cmd string, args []string) {
	err := exec.Command(cmd, args...).Run()
	if err != nil {
		fmt.Println(err.Error())
	}
}
