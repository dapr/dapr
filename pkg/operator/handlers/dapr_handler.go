package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/operator/monitoring"
	"github.com/dapr/dapr/pkg/validation"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	daprEnabledAnnotationKey        = "dapr.io/enabled"
	appIDAnnotationKey              = "dapr.io/app-id"
	daprMetricsPortKey              = "dapr.io/metrics-port"
	daprSidecarHTTPPortName         = "dapr-http"
	daprSidecarAPIGRPCPortName      = "dapr-grpc"
	daprSidecarInternalGRPCPortName = "dapr-internal"
	daprSidecarMetricsPortName      = "dapr-metrics"
	daprSidecarHTTPPort             = 3500
	daprSidecarAPIGRPCPort          = 50001
	daprSidecarInternalGRPCPort     = 50002
	defaultMetricsPort              = 9090
	clusterIPNone                   = "None"
	daprServiceOwnerField           = ".metadata.controller"
)

var log = logger.NewLogger("dapr.operator.handlers")

// DaprHandler handles the lifetime for Dapr CRDs
type DaprHandler struct {
	mgr ctrl.Manager

	client.Client
	Scheme *runtime.Scheme
}

// NewDaprHandler returns a new Dapr handler
func NewDaprHandler(mgr ctrl.Manager) *DaprHandler {
	return &DaprHandler{
		mgr: mgr,

		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
}

// Init allows for various startup tasks
func (h *DaprHandler) Init() error {
	if err := h.mgr.GetFieldIndexer().IndexField(
		&corev1.Service{}, daprServiceOwnerField, func(rawObj runtime.Object) []string {
			svc := rawObj.(*corev1.Service)
			owner := meta_v1.GetControllerOf(svc)
			if owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() || owner.Kind != "Deployment" {
				return nil
			}
			return []string{owner.Name}
		}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(h.mgr).
		For(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(h)
}

func (h *DaprHandler) daprServiceName(appID string) string {
	return fmt.Sprintf("%s-dapr", appID)
}

// Reconcile the expected services for deployments annotated for Dapr.
func (h *DaprHandler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()

	var deployment appsv1.Deployment
	expectedService := false
	if err := h.Get(ctx, req.NamespacedName, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debugf("deployment has be deleted, %s", req.NamespacedName)
		} else {
			log.Errorf("unable to get deployment, %s, err: %s", req.NamespacedName, err)
			return ctrl.Result{}, err
		}
	} else {
		if deployment.DeletionTimestamp != nil {
			log.Debugf("deployment is being deleted, %s", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		expectedService = h.isAnnotatedForDapr(&deployment)
	}

	if expectedService {
		if err := h.ensureDaprServicePresent(ctx, req.Namespace, &deployment); err != nil {
			return ctrl.Result{Requeue: true}, err
		}
	} else {
		if err := h.ensureDaprServiceAbsent(ctx, req.NamespacedName); err != nil {
			return ctrl.Result{Requeue: true}, err
		}
	}
	return ctrl.Result{}, nil
}

func (h *DaprHandler) ensureDaprServicePresent(ctx context.Context, namespace string, deployment *appsv1.Deployment) error {
	appID := h.getAppID(deployment)
	err := validation.ValidateKubernetesAppID(appID)
	if err != nil {
		return err
	}

	mayDaprService := types.NamespacedName{
		Namespace: namespace,
		Name:      h.daprServiceName(appID),
	}
	var daprSvc corev1.Service
	if err := h.Get(ctx, mayDaprService, &daprSvc); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debugf("no service for deployment found, deployment: %s/%s", namespace, deployment.Name)
			return h.createDaprService(ctx, mayDaprService, deployment)
		}
		log.Errorf("unable to get service, %s, err: %s", mayDaprService, err)
		return err
	}
	return nil
}

func (h *DaprHandler) createDaprService(ctx context.Context, expectedService types.NamespacedName, deployment *appsv1.Deployment) error {
	appID := h.getAppID(deployment)
	metricsPort := h.getMetricsPort(deployment)

	service := &corev1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      expectedService.Name,
			Namespace: expectedService.Namespace,
			Labels:    map[string]string{daprEnabledAnnotationKey: "true"},
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   strconv.Itoa(metricsPort),
				"prometheus.io/path":   "/",
				appIDAnnotationKey:     appID,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector:  deployment.Spec.Selector.MatchLabels,
			ClusterIP: clusterIPNone,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(daprSidecarHTTPPort),
					Name:       daprSidecarHTTPPortName,
				},
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(daprSidecarAPIGRPCPort),
					TargetPort: intstr.FromInt(daprSidecarAPIGRPCPort),
					Name:       daprSidecarAPIGRPCPortName,
				}, {
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(daprSidecarInternalGRPCPort),
					TargetPort: intstr.FromInt(daprSidecarInternalGRPCPort),
					Name:       daprSidecarInternalGRPCPortName,
				},
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       int32(metricsPort),
					TargetPort: intstr.FromInt(metricsPort),
					Name:       daprSidecarMetricsPortName,
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(deployment, service, h.Scheme); err != nil {
		return err
	}
	if err := h.Create(ctx, service); err != nil {
		log.Errorf("unable to create Dapr service for deployment, service: %s, err: %s", expectedService, err)
		return err
	}
	log.Debugf("created service: %s", expectedService)
	monitoring.RecordServiceCreatedCount(appID)
	return nil
}

func (h *DaprHandler) ensureDaprServiceAbsent(ctx context.Context, deploymentKey types.NamespacedName) error {
	var services corev1.ServiceList
	if err := h.List(ctx, &services,
		client.InNamespace(deploymentKey.Namespace),
		client.MatchingFields{daprServiceOwnerField: deploymentKey.Name}); err != nil {
		log.Errorf("unable to list services, err: %s", err)
		return err
	}
	for i := range services.Items {
		svc := services.Items[i] // Make a copy since we will refer to this as a reference in this loop.
		log.Debugf("deleting service: %s/%s", svc.Namespace, svc.Name)
		if err := h.Delete(ctx, &svc, client.PropagationPolicy(meta_v1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			log.Errorf("unable to delete svc: %s/%s, err: %s", svc.Namespace, svc.Name, err)
		} else {
			log.Debugf("deleted service: %s/%s", svc.Namespace, svc.Name)
			appID := svc.Annotations[appIDAnnotationKey]
			monitoring.RecordServiceDeletedCount(appID)
		}
	}
	return nil
}

func (h *DaprHandler) getAppID(deployment *appsv1.Deployment) string {
	annotations := deployment.Spec.Template.ObjectMeta.Annotations
	if val, ok := annotations[appIDAnnotationKey]; ok && val != "" {
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

func (h *DaprHandler) getMetricsPort(deployment *appsv1.Deployment) int {
	annotations := deployment.Spec.Template.ObjectMeta.Annotations
	metricsPort := defaultMetricsPort
	if val, ok := annotations[daprMetricsPortKey]; ok {
		if v, err := strconv.Atoi(val); err == nil {
			metricsPort = v
		}
	}
	return metricsPort
}
