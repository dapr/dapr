package handlers

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/util/intstr"

	log "github.com/Sirupsen/logrus"
	scheme "github.com/actionscore/actions/pkg/client/clientset/versioned"
	"github.com/actionscore/actions/pkg/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	actionsEnabledAnnotationKey = "actions.io/enabled"
	actionsIDAnnotationKey      = "actions.io/id"
	actionSidecarHTTPPortName   = "actions-http"
	actionSidecarGRPCPortName   = "actions-grpc"
	actionSidecarHTTPPort       = 3500
	actionSidecarGRPCPort       = 50001
)

// ActionsHandler handles the lifetime for Actions CRDs
type ActionsHandler struct {
	client          scheme.Interface
	deploymentsLock *sync.Mutex
}

// NewActionsHandler returns a new Actions handler
func NewActionsHandler(client scheme.Interface) *ActionsHandler {
	return &ActionsHandler{
		client:          client,
		deploymentsLock: &sync.Mutex{},
	}
}

// Init allows for various startup tasks
func (h *ActionsHandler) Init() error {
	return nil
}

func (h *ActionsHandler) createActionsService(name string, deployment *appsv1.Deployment) error {
	serviceName := fmt.Sprintf("%s-actions", name)
	exists := kubernetes.ServiceExists(serviceName, deployment.GetNamespace())
	if exists {
		log.Infof("service exists: %s", serviceName)
		return nil
	}

	service := &corev1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:   serviceName,
			Labels: map[string]string{actionsEnabledAnnotationKey: "true"},
		},
		Spec: corev1.ServiceSpec{
			Selector: deployment.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(actionSidecarHTTPPort),
					Name:       actionSidecarHTTPPortName,
				},
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(actionSidecarGRPCPort),
					TargetPort: intstr.FromInt(actionSidecarGRPCPort),
					Name:       actionSidecarGRPCPortName,
				},
			},
		},
	}

	err := kubernetes.CreateService(service, deployment.GetNamespace())
	if err != nil {
		return err
	}

	log.Infof("created service %s in namespace %s", serviceName, deployment.GetNamespace())
	return nil
}

func (h *ActionsHandler) getActionsID(deployment *appsv1.Deployment) string {
	annotations := deployment.Spec.Template.ObjectMeta.Annotations
	if val, ok := annotations[actionsIDAnnotationKey]; ok && val != "" {
		return val
	}

	return ""
}

func (h *ActionsHandler) isAnnotatedForActions(deployment *appsv1.Deployment) bool {
	annotations := deployment.Spec.Template.ObjectMeta.Annotations
	enabled, ok := annotations[actionsEnabledAnnotationKey]
	if !ok {
		return false
	}
	switch strings.ToLower(enabled) {
	case "y", "yes", "true", "on", "1":
		return true
	default:
		return false
	}
}

// ObjectCreated handles Actions enabled deployment state changes
func (h *ActionsHandler) ObjectCreated(obj interface{}) {
	h.deploymentsLock.Lock()
	defer h.deploymentsLock.Unlock()

	deployment := obj.(*appsv1.Deployment)
	annotated := h.isAnnotatedForActions(deployment)
	if annotated {
		id := h.getActionsID(deployment)
		if id == "" {
			log.Errorf("skipping service creation: id for deployment %s is empty", deployment.GetName())
			return
		}

		err := h.createActionsService(id, deployment)
		if err != nil {
			log.Errorf("failed creating service for deployment %s: %s", deployment.GetName(), err)
		}
	}
}

// ObjectUpdated handles Actions crd updates
func (h *ActionsHandler) ObjectUpdated(old interface{}, new interface{}) {
}

// ObjectDeleted handles Actions crd deletion
func (h *ActionsHandler) ObjectDeleted(obj interface{}) {
}
