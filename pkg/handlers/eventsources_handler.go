package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"

	"k8s.io/apimachinery/pkg/labels"

	log "github.com/Sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	actions_v1alpha1 "github.com/actionscore/actions/pkg/apis/eventing/v1alpha1"
	pb "github.com/actionscore/actions/pkg/proto"
)

type eventSourcePayload struct {
	Name string          `json:"name"`
	Spec eventSourceSpec `json:"spec"`
}

type eventSourceSpec struct {
	Type           string      `json:"type"`
	ConnectionInfo interface{} `json:"connectionInfo"`
}

type EventSourcesHandler struct {
	KubeClient kubernetes.Interface
}

func NewEventSourcesHandler(client kubernetes.Interface) *EventSourcesHandler {
	return &EventSourcesHandler{
		KubeClient: client,
	}
}

func (e *EventSourcesHandler) Init() error {
	log.Info("EventSourcesHandler.Init")
	return nil
}

func (r *EventSourcesHandler) ObjectUpdated(old interface{}, new interface{}) {
}

func (r *EventSourcesHandler) ObjectDeleted(obj interface{}) {
}

func (r *EventSourcesHandler) ObjectCreated(obj interface{}) {
	log.Info("Notified about an event source update")

	eventSource := obj.(*actions_v1alpha1.EventSource)
	err := r.PublishEventSourceToActions(eventSource)
	if err != nil {
		log.Errorf("Error from EventSourcesHandler: %s", err)
	}
}

func (r *EventSourcesHandler) PublishEventSourceToActions(eventSource *actions_v1alpha1.EventSource) error {
	payload := pb.EventSource{
		Name: eventSource.GetName(),
		Spec: &pb.EventSourceSpec{
			Type: eventSource.Spec.Type,
		},
	}

	b, err := json.Marshal(eventSource.Spec.ConnectionInfo)
	if err != nil {
		return err
	}

	payload.Spec.ConnectionInfo = &any.Any{Value: b}

	services, err := r.KubeClient.CoreV1().Services(meta_v1.NamespaceAll).List(meta_v1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{actionsEnabledAnnotationKey: actionsEnabledAnnotationValue}).String(),
	})
	if err != nil {
		return err
	}

	for _, s := range services.Items {
		svcName := s.GetName()

		log.Infof("Updating actions pod selected by service: %s", svcName)
		endpoints, err := r.KubeClient.CoreV1().Endpoints(s.GetNamespace()).Get(svcName, meta_v1.GetOptions{})
		if err != nil {
			log.Errorf("Error getting endpoints for service %s: %s", svcName, err)
			continue
		}
		go r.PublishEventSourceToService(payload, endpoints)
	}

	return nil
}

func (r *EventSourcesHandler) PublishEventSourceToService(eventSource pb.EventSource, endpoints *corev1.Endpoints) {
	if endpoints != nil && len(endpoints.Subsets) > 0 {
		for _, a := range endpoints.Subsets[0].Addresses {
			address := fmt.Sprintf("%s:%s", a.IP, fmt.Sprintf("%v", actionSidecarGRPCPort))
			go r.sendEventSourceToPod(eventSource, address)
		}
	}
}

func (r *EventSourcesHandler) sendEventSourceToPod(eventSource pb.EventSource, address string) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Errorf("gRPC connection failure: %s", err)
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	c := pb.NewActionClient(conn)
	c.UpdateEventSource(ctx, &eventSource)
}
