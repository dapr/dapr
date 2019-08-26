package handlers

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	"k8s.io/apimachinery/pkg/labels"

	log "github.com/Sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	components_v1alpha1 "github.com/actionscore/actions/pkg/apis/components/v1alpha1"
	pb "github.com/actionscore/actions/pkg/proto"
)

// ComponentsHandler handles the lifetime management of Component CRDs
type ComponentsHandler struct {
	kubeClient kubernetes.Interface
}

// NewComponentsHandler returns a new component handler
func NewComponentsHandler(client kubernetes.Interface) *ComponentsHandler {
	return &ComponentsHandler{
		kubeClient: client,
	}
}

// Init performs any startup tasks needed
func (c *ComponentsHandler) Init() error {
	return nil
}

// ObjectUpdated handles upated crd operations
func (c *ComponentsHandler) ObjectUpdated(old interface{}, new interface{}) {
}

// ObjectDeleted handles deleted crd operations
func (c *ComponentsHandler) ObjectDeleted(obj interface{}) {
}

// ObjectCreated handles created crd operations
func (c *ComponentsHandler) ObjectCreated(obj interface{}) {
	log.Info("notified about a component update")

	component := obj.(*components_v1alpha1.Component)
	err := c.publishComponentToActionsRuntimes(component)
	if err != nil {
		log.Errorf("error from ObjectCreated: %s", err)
	}
}

func (c *ComponentsHandler) publishComponentToActionsRuntimes(component *components_v1alpha1.Component) error {
	payload := pb.Component{
		Name: component.GetName(),
		Spec: &pb.ComponentSpec{
			Type:           component.Spec.Type,
			ConnectionInfo: component.Spec.ConnectionInfo,
			Properties:     component.Spec.Properties,
		},
	}

	services, err := c.kubeClient.CoreV1().Services(meta_v1.NamespaceAll).List(meta_v1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{actionsEnabledAnnotationKey: "true"}).String(),
	})
	if err != nil {
		return err
	}

	for _, s := range services.Items {
		svcName := s.GetName()

		log.Infof("updating actions pod selected by service: %s", svcName)
		endpoints, err := c.kubeClient.CoreV1().Endpoints(s.GetNamespace()).Get(svcName, meta_v1.GetOptions{})
		if err != nil {
			log.Errorf("error getting endpoints for service %s: %s", svcName, err)
			continue
		}
		go c.publishComponentToService(payload, endpoints)
	}

	return nil
}

func (c *ComponentsHandler) publishComponentToService(component pb.Component, endpoints *corev1.Endpoints) {
	if endpoints != nil && len(endpoints.Subsets) > 0 {
		for _, a := range endpoints.Subsets[0].Addresses {
			address := fmt.Sprintf("%s:%s", a.IP, fmt.Sprintf("%v", actionSidecarGRPCPort))
			go c.updateActionsRuntime(component, address)
		}
	}
}

func (c *ComponentsHandler) updateActionsRuntime(component pb.Component, address string) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Errorf("gRPC connection failure: %s", err)
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	client := pb.NewActionsClient(conn)
	_, err = client.UpdateComponent(ctx, &component)
	if err != nil {
		log.Warnf("error updating Actions Runtime with component: %s", err)
	}
}
