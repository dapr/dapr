package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/operator/monitoring"
	"github.com/dapr/dapr/pkg/validation"
)

const (
	daprEnabledAnnotationKey        = "dapr.io/enabled"
	appIDAnnotationKey              = "dapr.io/app-id"
	daprEnableMetricsKey            = "dapr.io/enable-metrics"
	daprMetricsPortKey              = "dapr.io/metrics-port"
	daprSidecarHTTPPortName         = "dapr-http"
	daprSidecarAPIGRPCPortName      = "dapr-grpc"
	daprSidecarInternalGRPCPortName = "dapr-internal"
	daprSidecarMetricsPortName      = "dapr-metrics"
	daprSidecarHTTPPort             = 3500
	daprSidecarAPIGRPCPort          = 50001
	daprSidecarInternalGRPCPort     = 50002
	defaultMetricsEnabled           = true
	defaultMetricsPort              = 9090
	clusterIPNone                   = "None"
	daprServiceOwnerField           = ".metadata.controller"
)

var log = logger.NewLogger("dapr.operator.handlers")

// DaprHandler handles the lifetime for Dapr CRDs.
type DaprHandler struct {
	mgr ctrl.Manager

	client.Client
	Scheme *runtime.Scheme
}

// NewDaprHandler returns a new Dapr handler.
func NewDaprHandler(mgr ctrl.Manager) *DaprHandler {
	return &DaprHandler{
		mgr: mgr,

		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
}

// Init allows for various startup tasks.
func (h *DaprHandler) Init() error {
	if err := h.mgr.GetFieldIndexer().IndexField(
		context.TODO(),
		&corev1.Service{}, daprServiceOwnerField, func(rawObj client.Object) []string {
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
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 100,
		}).
		Complete(h)
}

func (h *DaprHandler) daprServiceName(appID string) string {
	return fmt.Sprintf("%s-dapr", appID)
}

// Reconcile the expected services for deployments annotated for Dapr.
func (h *DaprHandler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	service := h.createDaprServiceValues(ctx, expectedService, deployment, appID)

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

func (h *DaprHandler) createDaprServiceValues(ctx context.Context, expectedService types.NamespacedName, deployment *appsv1.Deployment, appID string) *corev1.Service {
	enableMetrics := h.getEnableMetrics(deployment)
	metricsPort := h.getMetricsPort(deployment)
	log.Debugf("enableMetrics: %v", enableMetrics)

	annotations := map[string]string{
		appIDAnnotationKey: appID,
	}

	if enableMetrics {
		annotations["prometheus.io/scrape"] = "true"
		annotations["prometheus.io/port"] = strconv.Itoa(metricsPort)
		annotations["prometheus.io/path"] = "/"
	}

	return &corev1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:        expectedService.Name,
			Namespace:   expectedService.Namespace,
			Labels:      map[string]string{daprEnabledAnnotationKey: "true"},
			Annotations: annotations,
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
				},
				{
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

func (h *DaprHandler) getEnableMetrics(deployment *appsv1.Deployment) bool {
	annotations := deployment.Spec.Template.ObjectMeta.Annotations
	enableMetrics := defaultMetricsEnabled
	if val, ok := annotations[daprEnableMetricsKey]; ok {
		if v, err := strconv.ParseBool(val); err == nil {
			enableMetrics = v
		}
	}
	return enableMetrics
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
