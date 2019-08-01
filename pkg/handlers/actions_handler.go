package handlers

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/util/intstr"

	log "github.com/Sirupsen/logrus"
	scheme "github.com/actionscore/actions/pkg/client/clientset/versioned"
	"github.com/actionscore/actions/pkg/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Protocol is a communication protocol type
type Protocol string

const (
	actionsEnabledAnnotationKey   = "actions.io/enabled"
	actionsIDAnnotationKey        = "actions.io/id"
	actionsProtocolAnnotationKey  = "actions.io/protocol"
	actionsConfigAnnotationKey    = "actions.io/config"
	actionsEnabledAnnotationValue = "true"
	actionSidecarContainerName    = "actionsrt"
	actionSidecarHTTPPortName     = "actions-http"
	actionSidecarGRPCPortName     = "actions-grpc"
	actionSidecarHTTPPort         = 3500
	actionSidecarGRPCPort         = 50001
	apiAddress                    = "http://actions-api"
	placementAddress              = "actions-placement"
	// HTTPProtocol represents an http protocol
	HTTPProtocol Protocol = "http"
	// GRPCProtocol represents a grpc protocol
	GRPCProtocol Protocol = "grpc"
)

// ActionsHandler handles the lifetime for Actions CRDs
type ActionsHandler struct {
	Client          scheme.Interface
	DeploymentsLock *sync.Mutex
	Config          ActionsHandlerConfig
}

// Config object for the handler
type ActionsHandlerConfig struct {
	RuntimeImage        string
	ImagePullSecretName string
	Namespace           string
}

// NewActionsHandler returns a new Actions handler
func NewActionsHandler(client scheme.Interface, config ActionsHandlerConfig) *ActionsHandler {
	return &ActionsHandler{
		Config:          config,
		Client:          client,
		DeploymentsLock: &sync.Mutex{},
	}
}

// Init allows for various startup tasks
func (r *ActionsHandler) Init() error {
	log.Info("ActionsHandler.Init")
	return nil
}

// GetEventingSidecar creates a container for the Actions runtime
func (r *ActionsHandler) GetEventingSidecar(applicationPort, applicationProtocol, actionName, config, actionSidecarImage, namespace string) v1.Container {
	return v1.Container{
		Name:            actionSidecarContainerName,
		Image:           actionSidecarImage,
		ImagePullPolicy: corev1.PullAlways,
		Ports: []v1.ContainerPort{
			{
				ContainerPort: int32(actionSidecarHTTPPort),
				Name:          actionSidecarHTTPPortName,
			},
			{
				ContainerPort: int32(actionSidecarGRPCPort),
				Name:          actionSidecarGRPCPortName,
			},
		},
		Command: []string{"/actionsrt"},
		Env:     []v1.EnvVar{{Name: "HOST_IP", ValueFrom: &v1.EnvVarSource{FieldRef: &v1.ObjectFieldSelector{FieldPath: "status.podIP"}}}, {Name: "NAMESPACE", Value: namespace}},
		Args:    []string{"--mode", "kubernetes", "--actions-http-port", fmt.Sprintf("%v", actionSidecarHTTPPort), "--actions-grpc-port", fmt.Sprintf("%v", actionSidecarGRPCPort), "--app-port", applicationPort, "--actions-id", actionName, "--control-plane-address", fmt.Sprintf("%s.%s.svc.cluster.local", apiAddress, r.Config.Namespace), "--protocol", applicationProtocol, "--placement-address", fmt.Sprintf("%s.%s.svc.cluster.local", placementAddress, r.Config.Namespace), "--config", config},
	}
}

// GetApplicationPort returns the port used by the app
func (r *ActionsHandler) GetApplicationPort(containers []v1.Container) string {
	for _, c := range containers {
		if (c.Name != actionSidecarHTTPPortName && c.Name != actionSidecarGRPCPortName) && len(c.Ports) > 0 {
			return fmt.Sprint(c.Ports[0].ContainerPort)
		}
	}

	return ""
}

// CreateEventingService creates a new kubernetes service that points to the Actions runtime container
func (r *ActionsHandler) CreateEventingService(name string, deployment *appsv1.Deployment) error {
	serviceName := fmt.Sprintf("%s-%s", name, "action")
	exists := kubernetes.ServiceExists(serviceName, deployment.ObjectMeta.Namespace)
	if exists {
		log.Infof("Service exists: %s", serviceName)
		return nil
	}

	service := &corev1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:   serviceName,
			Labels: map[string]string{actionsEnabledAnnotationKey: actionsEnabledAnnotationValue},
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

	log.Infof("Created service: %s", serviceName)

	return nil
}

// GetActionName gets the unique Actions ID from deployment annotations
func (r *ActionsHandler) GetActionName(deployment *appsv1.Deployment) string {
	annotations := deployment.ObjectMeta.GetAnnotations()
	if val, ok := annotations[actionsIDAnnotationKey]; ok && val != "" {
		return val
	}

	return deployment.GetName()
}

// GetAppProtocol returns a new protocol. either http or grpc
func (r *ActionsHandler) GetAppProtocol(deployment *appsv1.Deployment) string {
	if val, ok := deployment.ObjectMeta.Annotations[actionsProtocolAnnotationKey]; ok && val != "" {
		if Protocol(val) != HTTPProtocol && Protocol(val) != GRPCProtocol {
			return string(HTTPProtocol)
		}

		return val
	}

	return string(HTTPProtocol)
}

// GetAppConfig returns an actions config annotation value
func (r *ActionsHandler) GetAppConfig(deployment *appsv1.Deployment) string {
	if val, ok := deployment.ObjectMeta.Annotations[actionsConfigAnnotationKey]; ok && val != "" {
		return val
	}

	return ""
}

// IsAnnotatedForActions checks whether a deployment has Actions annotation
func (r *ActionsHandler) IsAnnotatedForActions(deployment *appsv1.Deployment) bool {
	annotations := deployment.ObjectMeta.Annotations
	if annotations != nil {
		if val, ok := annotations[actionsEnabledAnnotationKey]; ok && val == actionsEnabledAnnotationValue {
			return true
		}
	}

	return false
}

// ActionEnabled checks whether the Actions sidecar is running inside a pod
func (r *ActionsHandler) ActionEnabled(deployment *appsv1.Deployment) bool {
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name == actionSidecarContainerName {
			return true
		}
	}

	return false
}

// EnableAction creates an Actions sidecar and updates a deployment
func (r *ActionsHandler) EnableAction(deployment *appsv1.Deployment) error {
	appPort := r.GetApplicationPort(deployment.Spec.Template.Spec.Containers)
	appProtocol := r.GetAppProtocol(deployment)
	config := r.GetAppConfig(deployment)
	actionName := r.GetActionName(deployment)
	sidecar := r.GetEventingSidecar(appPort, appProtocol, actionName, config, r.Config.RuntimeImage, deployment.ObjectMeta.Namespace)
	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, sidecar)

	if r.Config.ImagePullSecretName != "" {
		deployment.Spec.Template.Spec.ImagePullSecrets = append(deployment.Spec.Template.Spec.ImagePullSecrets, corev1.LocalObjectReference{
			Name: r.Config.ImagePullSecretName,
		})
	}

	err := r.CreateEventingService(actionName, deployment)
	if err != nil {
		return err
	}

	err = kubernetes.UpdateDeployment(deployment)
	if err != nil {
		return err
	}

	return nil
}

// RemoveActionFromDeployment removes the Actions container from a deployment
func (r *ActionsHandler) RemoveActionFromDeployment(deployment *appsv1.Deployment) error {
	for i := len(deployment.Spec.Template.Spec.Containers) - 1; i >= 0; i-- {
		if deployment.Spec.Template.Spec.Containers[i].Name == actionSidecarContainerName {
			deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers[:i], deployment.Spec.Template.Spec.Containers[i+1:]...)
		}
	}

	err := kubernetes.UpdateDeployment(deployment)
	if err != nil {
		return err
	}

	return nil
}

// ObjectCreated handles Actions crds creation
func (r *ActionsHandler) ObjectCreated(obj interface{}) {
	r.DeploymentsLock.Lock()

	deployment := obj.(*appsv1.Deployment)
	name := deployment.ObjectMeta.Name

	actionEnabled := r.ActionEnabled(deployment)
	annotated := r.IsAnnotatedForActions(deployment)

	if annotated && !actionEnabled {
		log.Infof("Notified of action annotated deployment %s: Starting action", name)
		err := r.EnableAction(deployment)
		if err != nil {
			log.Errorf("Error enabling action: %s", err)
		} else {
			log.Infof("Action enabled successfully for deployment %s", name)
		}
	} else if !annotated && actionEnabled {
		err := r.RemoveActionFromDeployment(deployment)
		if err != nil {
			log.Errorf("Error removing action from deployment %s: %s", name, err)
		} else {
			log.Infof("Action removed from deployment %s", name)
		}
	}

	r.DeploymentsLock.Unlock()
}

// ObjectDeleted handles Actions crd updates
func (r *ActionsHandler) ObjectUpdated(old interface{}, new interface{}) {
}

// ObjectDeleted handles Actions crd deletion
func (r *ActionsHandler) ObjectDeleted(obj interface{}) {
}
