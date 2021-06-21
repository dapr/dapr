package kubernetes

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/phayes/freeport"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PodPortFowarder implements the PortForwarder interface for Kubernetes.
type PodPortForwarder struct {
	// Kubernetes client
	client *KubeClient
	// Kubernetes namespace
	namespace string
	// stopChannel is the channel used to manage the port forward lifecycle
	stopChannel chan struct{}
	// readyChannel communicates when the tunnel is ready to receive traffic
	readyChannel chan struct{}
}

// PortForwardRequest encapsulates data required to establish a Kuberentes tunnel.
type PortForwardRequest struct {
	// restConfig is the kubernetes config
	restConfig *rest.Config
	// pod is the selected pod for this port forwarding
	pod apiv1.Pod
	// localPort is the local port that will be selected to forward the PodPort
	localPorts []int
	// podPort is the target port for the pod
	podPorts []int
	// streams configures where to write or read input from
	streams genericclioptions.IOStreams
	// stopChannel is the channel used to manage the port forward lifecycle
	stopChannel chan struct{}
	// stopChannel communicates when the tunnel is ready to receive traffic
	readyChannel chan struct{}
}

// NewPodPortForwarder returns a new PodPortForwarder.
func NewPodPortForwarder(c *KubeClient, namespace string) *PodPortForwarder {
	return &PodPortForwarder{
		client:       c,
		namespace:    namespace,
		readyChannel: make(chan struct{}),
		stopChannel:  make(chan struct{}),
	}
}

// Connect establishes a new connection to a given app on the provided target ports.
func (p *PodPortForwarder) Connect(name string, targetPorts ...int) ([]int, error) {
	if name == "" {
		return nil, fmt.Errorf("name must be set to establish connection")
	}
	if len(targetPorts) == 0 {
		return nil, fmt.Errorf("cannot establish connection without target ports")
	}
	if p.namespace == "" {
		return nil, fmt.Errorf("namespace must be set to establish connection")
	}
	if p.client == nil {
		return nil, fmt.Errorf("client must be set to establish connection")
	}

	config := p.client.GetClientConfig()

	var ports []int
	for i := 0; i < len(targetPorts); i++ {
		p, perr := freeport.GetFreePort()
		if perr != nil {
			return nil, perr
		}
		ports = append(ports, p)
	}

	streams := genericclioptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	err := startPortForwarding(PortForwardRequest{
		restConfig: config,
		pod: apiv1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: p.namespace,
			},
		},
		localPorts:   ports,
		podPorts:     targetPorts,
		streams:      streams,
		stopChannel:  p.stopChannel,
		readyChannel: p.readyChannel,
	})
	if err != nil {
		return nil, err
	}

	<-p.readyChannel

	return ports, nil
}

func (p *PodPortForwarder) Close() error {
	if p.stopChannel != nil {
		close(p.stopChannel)
	}
	return nil
}

func startPortForwarding(req PortForwardRequest) error {
	// create spdy roundtripper
	roundTripper, upgrader, err := spdy.RoundTripperFor(req.restConfig)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", req.pod.Namespace, req.pod.Name)
	serverURL, _ := url.Parse(req.restConfig.Host)
	serverURL.Scheme = "https"
	serverURL.Path = path

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, serverURL)

	var ports []string //nolint: prealloc
	for i, p := range req.podPorts {
		ports = append(ports, fmt.Sprintf("%d:%d", req.localPorts[i], p))
	}
	fw, err := portforward.New(dialer, ports, req.stopChannel, req.readyChannel, req.streams.Out, req.streams.ErrOut)
	if err != nil {
		return err
	}

	go func() {
		if err = fw.ForwardPorts(); err != nil {
			log.Printf("Error closing port fowarding: %+v", err)
			// TODO: How to handle error?
		}
		log.Println("Closed port fowarding")
	}()
	return nil
}
