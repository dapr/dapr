package handlers

import (
	"fmt"
	"strings"
	"sync"

	"github.com/dapr/dapr/pkg/kubernetes"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	daprEnabledAnnotationKey = "dapr.io/enabled"
	daprIDAnnotationKey      = "dapr.io/id"
	daprSidecarHTTPPortName  = "dapr-http"
	daprSidecarGRPCPortName  = "dapr-grpc"
	daprSidecarHTTPPort      = 3500
	daprSidecarGRPCPort      = 50001
)

// DaprHandler handles the lifetime for Dapr CRDs
type DaprHandler struct {
	kubeAPI         *kubernetes.API
	deploymentsLock *sync.Mutex
}

// NewDaprHandler returns a new Dapr handler
func NewDaprHandler(kubeAPI *kubernetes.API) *DaprHandler {
	return &DaprHandler{
		kubeAPI:         kubeAPI,
		deploymentsLock: &sync.Mutex{},
	}
}

// Init allows for various startup tasks
func (h *DaprHandler) Init() error {
	return nil
}

func (h *DaprHandler) createDaprService(name string, deployment *appsv1.Deployment) error {
	serviceName := fmt.Sprintf("%s-dapr", name)
	exists := h.kubeAPI.ServiceExists(serviceName, deployment.GetNamespace())
	if exists {
		log.Infof("service exists: %s", serviceName)
		return nil
	}

	service := &corev1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:   serviceName,
			Labels: map[string]string{daprEnabledAnnotationKey: "true"},
		},
		Spec: corev1.ServiceSpec{
			Selector: deployment.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(daprSidecarHTTPPort),
					Name:       daprSidecarHTTPPortName,
				},
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(daprSidecarGRPCPort),
					TargetPort: intstr.FromInt(daprSidecarGRPCPort),
					Name:       daprSidecarGRPCPortName,
				},
			},
		},
	}

	err := h.kubeAPI.CreateService(service, deployment.GetNamespace())
	if err != nil {
		return err
	}

	log.Infof("created service %s in namespace %s", serviceName, deployment.GetNamespace())
	return nil
}

func (h *DaprHandler) deleteDaprService(name string, deployment *appsv1.Deployment) error {
	serviceName := fmt.Sprintf("%s-dapr", name)
	exists := h.kubeAPI.ServiceExists(serviceName, deployment.GetNamespace())
	if !exists {
		log.Infof("service does not exist: %s", serviceName)
		return nil
	}

	err := h.kubeAPI.DeleteService(serviceName, deployment.GetNamespace())
	if err != nil {
		return err
	}

	log.Infof("deleted service %s in namespace %s", serviceName, deployment.GetNamespace())
	return nil
}

func (h *DaprHandler) getDaprID(deployment *appsv1.Deployment) string {
	annotations := deployment.Spec.Template.ObjectMeta.Annotations
	if val, ok := annotations[daprIDAnnotationKey]; ok && val != "" {
		return val
	}

	return ""
}

func (h *DaprHandler) isAnnotatedForDapr(deployment *appsv1.Deployment) bool {
	annotations := deployment.Spec.Template.ObjectMeta.Annotations
	enabled, ok := annotations[daprEnabledAnnotationKey]
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

// ObjectCreated handles Dapr enabled deployment state changes
func (h *DaprHandler) ObjectCreated(obj interface{}) {
	h.deploymentsLock.Lock()
	defer h.deploymentsLock.Unlock()

	deployment := obj.(*appsv1.Deployment)
	annotated := h.isAnnotatedForDapr(deployment)
	if annotated {
		id := h.getDaprID(deployment)
		if id == "" {
			log.Errorf("skipping service creation: id for deployment %s is empty", deployment.GetName())
			return
		}

		err := h.createDaprService(id, deployment)
		if err != nil {
			log.Errorf("failed creating service for deployment %s: %s", deployment.GetName(), err)
		}
	}
}

// ObjectUpdated handles Dapr crd updates
func (h *DaprHandler) ObjectUpdated(old interface{}, new interface{}) {
}

// ObjectDeleted handles Dapr crd deletion
func (h *DaprHandler) ObjectDeleted(obj interface{}) {
	h.deploymentsLock.Lock()
	defer h.deploymentsLock.Unlock()

	deployment := obj.(*appsv1.Deployment)
	annotated := h.isAnnotatedForDapr(deployment)
	if annotated {
		id := h.getDaprID(deployment)
		if id == "" {
			log.Warnf("skipping service deletion: id for deployment %s is empty", deployment.GetName())
			return
		}

		err := h.deleteDaprService(id, deployment)
		if err != nil {
			log.Errorf("failed deleting service for deployment %s: %s", deployment.GetName(), err)
		}
	}
}
