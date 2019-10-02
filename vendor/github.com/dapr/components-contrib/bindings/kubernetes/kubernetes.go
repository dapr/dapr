package kubernetes

import (
	"context"
	"encoding/json"

	"github.com/dapr/components-contrib/bindings"
	"github.com/kubernetes-client/go/kubernetes/client"
	"github.com/kubernetes-client/go/kubernetes/config"
)

type kubernetesInput struct {
	config    *client.Configuration
	client    *client.APIClient
	namespace string
}

var _ = bindings.InputBinding(&kubernetesInput{})

// NewKubernetes returns a new Kubernetes event input binding
func NewKubernetes() bindings.InputBinding {
	return &kubernetesInput{}
}

func (k *kubernetesInput) Init(metadata bindings.Metadata) error {
	c, err := config.LoadKubeConfig()
	if err != nil {
		return err
	}
	k.config = c
	k.client = client.NewAPIClient(c)
	return k.parseMetadata(metadata)
}

func (k *kubernetesInput) parseMetadata(metadata bindings.Metadata) error {
	ns, found := metadata.Properties["namespace"]
	if found {
		k.namespace = ns
	} else {
		k.namespace = "default"
	}

	return nil
}

func (k *kubernetesInput) Read(handler func(*bindings.ReadResponse) error) error {
	watch := client.WatchClient{
		Cfg:     k.config,
		Client:  k.client,
		Path:    "/api/v1/namespaces/" + k.namespace + "/events",
		MakerFn: func() interface{} { return &client.V1Event{} },
	}

	resultChan, errChan, err := watch.Connect(context.Background(), "")
	if err != nil {
		return err
	}
	done := false
	for !done {
		select {
		case obj, ok := <-resultChan:
			if !ok {
				done = true
				break
			}
			data, err := json.Marshal(obj)
			if err != nil {
				return err
			}
			handler(&bindings.ReadResponse{
				Data: data,
			})
		case err := <-errChan:
			return err
		}
	}
	return nil
}
