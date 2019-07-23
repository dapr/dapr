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

type Protocol string

const (
	actionsEnabledAnnotationKey            = "actions.io/enabled"
	actionsIDAnnotationKey                 = "actions.io/id"
	actionsProtocolAnnotationKey           = "actions.io/protocol"
	actionsConfigAnnotationKey             = "actions.io/config"
	actionsEnabledAnnotationValue          = "true"
	actionSidecarContainerName             = "action"
	actionSidecarHTTPPortName              = "actions-http"
	actionSidecarGRPCPortName              = "actions-grpc"
	actionSidecarHTTPPort                  = 3500
	actionSidecarGRPCPort                  = 50001
	apiAddress                             = "http://actions-api.default.svc.cluster.local"
	placementAddress                       = "actions-placement.default.svc.cluster.local:80"
	HTTPProtocol                  Protocol = "http"
	GRPCProtocol                  Protocol = "grpc"
)

type ActionsHandler struct {
	Client          scheme.Interface
	DeploymentsLock *sync.Mutex
	Config          ActionsHandlerConfig
}

type ActionsHandlerConfig struct {
	RuntimeImage        string
	ImagePullSecretName string
}

func NewActionsHandler(client scheme.Interface, config ActionsHandlerConfig) *ActionsHandler {
	return &ActionsHandler{
		Config:          config,
		Client:          client,
		DeploymentsLock: &sync.Mutex{},
	}
}

func (r *ActionsHandler) Init() error {
	log.Info("ActionsHandler.Init")
	return nil
}

func (r *ActionsHandler) GetEventingSidecar(applicationPort, applicationProtocol, actionName, config, actionSidecarImage string) v1.Container {
	return v1.Container{
		Name:            actionSidecarContainerName,
		Image:           actionSidecarImage,
		ImagePullPolicy: corev1.PullAlways,
		Ports: []v1.ContainerPort{
			v1.ContainerPort{
				ContainerPort: int32(actionSidecarHTTPPort),
				Name:          actionSidecarHTTPPortName,
			},
			v1.ContainerPort{
				ContainerPort: int32(actionSidecarGRPCPort),
				Name:          actionSidecarGRPCPortName,
			},
		},
		Command: []string{"/actionsrt"},
		Env:     []v1.EnvVar{v1.EnvVar{Name: "HOST_IP", ValueFrom: &v1.EnvVarSource{FieldRef: &v1.ObjectFieldSelector{FieldPath: "status.podIP"}}}},
		Args:    []string{"--mode", "kubernetes", "--actions-http-port", fmt.Sprintf("%v", actionSidecarHTTPPort), "--actions-grpc-port", fmt.Sprintf("%v", actionSidecarGRPCPort), "--app-port", applicationPort, "--actions-id", actionName, "--control-plane-address", apiAddress, "--protocol", applicationProtocol, "--placement-address", placementAddress, "--config", config},
	}
}

func (r *ActionsHandler) GetApplicationPort(containers []v1.Container) string {
	for _, c := range containers {
		if (c.Name != actionSidecarHTTPPortName && c.Name != actionSidecarGRPCPortName) && len(c.Ports) > 0 {
			return fmt.Sprint(c.Ports[0].ContainerPort)
		}
	}

	return ""
}

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
				corev1.ServicePort{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(actionSidecarHTTPPort),
					Name:       actionSidecarHTTPPortName,
				},
				corev1.ServicePort{
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

func (r *ActionsHandler) GetActionName(deployment *appsv1.Deployment) string {
	annotations := deployment.ObjectMeta.GetAnnotations()
	if val, ok := annotations[actionsIDAnnotationKey]; ok && val != "" {
		return val
	}

	return deployment.GetName()
}

func (r *ActionsHandler) GetAppProtocol(deployment *appsv1.Deployment) string {
	if val, ok := deployment.ObjectMeta.Annotations[actionsProtocolAnnotationKey]; ok && val != "" {
		if Protocol(val) != HTTPProtocol && Protocol(val) != GRPCProtocol {
			return string(HTTPProtocol)
		}

		return val
	}

	return string(HTTPProtocol)
}

func (r *ActionsHandler) GetAppConfig(deployment *appsv1.Deployment) string {
	if val, ok := deployment.ObjectMeta.Annotations[actionsConfigAnnotationKey]; ok && val != "" {
		return val
	}

	return ""
}

func (r *ActionsHandler) IsAnnotatedForActions(deployment *appsv1.Deployment) bool {
	annotations := deployment.ObjectMeta.Annotations
	if annotations != nil {
		if val, ok := annotations[actionsEnabledAnnotationKey]; ok && val == actionsEnabledAnnotationValue {
			return true
		}
	}

	return false
}

func (r *ActionsHandler) ActionEnabled(deployment *appsv1.Deployment) bool {
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name == actionSidecarContainerName {
			return true
		}
	}

	return false
}

func (r *ActionsHandler) EnableAction(deployment *appsv1.Deployment) error {
	appPort := r.GetApplicationPort(deployment.Spec.Template.Spec.Containers)
	appProtocol := r.GetAppProtocol(deployment)
	config := r.GetAppConfig(deployment)
	actionName := r.GetActionName(deployment)
	sidecar := r.GetEventingSidecar(appPort, appProtocol, actionName, config, r.Config.RuntimeImage)
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

func (r *ActionsHandler) ObjectUpdated(old interface{}, new interface{}) {
}

func (r *ActionsHandler) ObjectDeleted(obj interface{}) {
}
