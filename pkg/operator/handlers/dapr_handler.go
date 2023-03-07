package handlers

import (
	"context"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/operator/meta"
	"github.com/dapr/dapr/pkg/operator/monitoring"
	"github.com/dapr/dapr/pkg/validation"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

const (
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
	annotationPrometheusProbe       = "prometheus.io/probe"
	annotationPrometheusScrape      = "prometheus.io/scrape"
	annotationPrometheusPort        = "prometheus.io/port"
	annotationPrometheusPath        = "prometheus.io/path"
)

var log = logger.NewLogger("dapr.operator.handlers")

// DaprHandler handles the lifetime for Dapr CRDs.
type DaprHandler struct {
	mgr ctrl.Manager

	client.Client
	Scheme *runtime.Scheme
}

type Reconciler struct {
	*DaprHandler
	newWrapper func() ObjectWrapper
}

// NewDaprHandler returns a new Dapr handler.
// This is a reconciler that watches all Deployment and StatefulSet resources and ensures that a matching Service resource is deployed to allow Dapr sidecar-to-sidecar communication and access to other ports.
func NewDaprHandler(mgr ctrl.Manager) *DaprHandler {
	return &DaprHandler{
		mgr: mgr,

		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
}

// Init allows for various startup tasks.
func (h *DaprHandler) Init() error {
	err := h.mgr.GetFieldIndexer().IndexField(
		context.TODO(),
		&corev1.Service{},
		daprServiceOwnerField,
		func(rawObj client.Object) []string {
			svc := rawObj.(*corev1.Service)
			owner := metaV1.GetControllerOf(svc)
			if owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() || (owner.Kind != "Deployment" && owner.Kind != "StatefulSet") {
				return nil
			}
			return []string{owner.Name}
		},
	)
	if err != nil {
		return err
	}

	err = ctrl.NewControllerManagedBy(h.mgr).
		For(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 100,
		}).
		Complete(&Reconciler{
			DaprHandler: h,
			newWrapper: func() ObjectWrapper {
				return &DeploymentWrapper{}
			},
		})
	if err != nil {
		return err
	}

	err = ctrl.NewControllerManagedBy(h.mgr).
		For(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 100,
		}).
		Complete(&Reconciler{
			DaprHandler: h,
			newWrapper: func() ObjectWrapper {
				return &StatefulSetWrapper{}
			},
		})
	if err != nil {
		return err
	}

	return nil
}

func (h *DaprHandler) daprServiceName(appID string) string {
	return appID + "-dapr"
}

// Reconcile the expected services for Deployment and StatefulSet resources annotated for Dapr.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// var wrapper appsv1.Deployment | appsv1.StatefulSet
	wrapper := r.newWrapper()

	expectedService := false
	err := r.Get(ctx, req.NamespacedName, wrapper.GetObject())
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Debugf("deployment has be deleted, %s", req.NamespacedName)
		} else {
			log.Errorf("unable to get deployment, %s, err: %s", req.NamespacedName, err)
			return ctrl.Result{}, err
		}
	} else {
		if wrapper.GetObject().GetDeletionTimestamp() != nil {
			log.Debugf("deployment is being deleted, %s", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		expectedService = r.isAnnotatedForDapr(wrapper)
	}

	if expectedService {
		err := r.ensureDaprServicePresent(ctx, req.Namespace, wrapper)
		if err != nil {
			return ctrl.Result{Requeue: true}, err
		}
	}

	return ctrl.Result{}, nil
}

func (h *DaprHandler) ensureDaprServicePresent(ctx context.Context, namespace string, wrapper ObjectWrapper) error {
	appID := h.getAppID(wrapper)
	err := validation.ValidateKubernetesAppID(appID)
	if err != nil {
		return err
	}

	daprSvcName := types.NamespacedName{
		Namespace: namespace,
		Name:      h.daprServiceName(appID),
	}
	var daprSvc corev1.Service
	err = h.Get(ctx, daprSvcName, &daprSvc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Debugf("no service for wrapper found, wrapper: %s/%s", namespace, daprSvcName.Name)
			return h.createDaprService(ctx, daprSvcName, wrapper)
		}
		log.Errorf("unable to get service, %s, err: %s", daprSvcName, err)
		return err
	}

	err = h.patchDaprService(ctx, daprSvcName, wrapper, daprSvc)
	if err != nil {
		log.Errorf("unable to update service, %s, err: %s", daprSvcName, err)
		return err
	}

	return nil
}

func (h *DaprHandler) patchDaprService(ctx context.Context, expectedService types.NamespacedName, wrapper ObjectWrapper, daprSvc corev1.Service) error {
	appID := h.getAppID(wrapper)
	service := h.createDaprServiceValues(ctx, expectedService, wrapper, appID)

	err := ctrl.SetControllerReference(wrapper.GetObject(), service, h.Scheme)
	if err != nil {
		return err
	}

	service.ObjectMeta.ResourceVersion = daprSvc.ObjectMeta.ResourceVersion

	err = h.Update(ctx, service)
	if err != nil {
		return err
	}

	monitoring.RecordServiceUpdatedCount(appID)
	return nil
}

func (h *DaprHandler) createDaprService(ctx context.Context, expectedService types.NamespacedName, wrapper ObjectWrapper) error {
	appID := h.getAppID(wrapper)
	service := h.createDaprServiceValues(ctx, expectedService, wrapper, appID)

	err := ctrl.SetControllerReference(wrapper.GetObject(), service, h.Scheme)
	if err != nil {
		return err
	}
	err = h.Create(ctx, service)
	if err != nil {
		log.Errorf("unable to create Dapr service for wrapper, service: %s, err: %s", expectedService, err)
		return err
	}
	log.Debugf("created service: %s", expectedService)
	monitoring.RecordServiceCreatedCount(appID)
	return nil
}

func (h *DaprHandler) createDaprServiceValues(ctx context.Context, expectedService types.NamespacedName, wrapper ObjectWrapper, appID string) *corev1.Service {
	enableMetrics := h.getEnableMetrics(wrapper)
	metricsPort := h.getMetricsPort(wrapper)
	log.Debugf("enableMetrics: %v", enableMetrics)

	annotationsMap := map[string]string{
		annotations.KeyAppID: appID,
	}

	if enableMetrics {
		annotationsMap[annotationPrometheusProbe] = "true"
		annotationsMap[annotationPrometheusScrape] = "true" // WARN: deprecated as of v1.7 please use prometheus.io/probe instead.
		annotationsMap[annotationPrometheusPort] = strconv.Itoa(metricsPort)
		annotationsMap[annotationPrometheusPath] = "/"
	}

	return &corev1.Service{
		ObjectMeta: metaV1.ObjectMeta{
			Name:        expectedService.Name,
			Namespace:   expectedService.Namespace,
			Labels:      map[string]string{annotations.KeyEnabled: "true"},
			Annotations: annotationsMap,
		},
		Spec: corev1.ServiceSpec{
			Selector:  wrapper.GetMatchLabels(),
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

func (h *DaprHandler) getAppID(wrapper ObjectWrapper) string {
	annotationsMap := wrapper.GetTemplateAnnotations()
	return annotationsMap[annotations.KeyAppID]
}

func (h *DaprHandler) isAnnotatedForDapr(wrapper ObjectWrapper) bool {
	return meta.IsAnnotatedForDapr(wrapper.GetTemplateAnnotations())
}

func (h *DaprHandler) getEnableMetrics(wrapper ObjectWrapper) bool {
	annotationsMap := wrapper.GetTemplateAnnotations()
	enableMetrics := defaultMetricsEnabled
	if val := annotationsMap[annotations.KeyEnableMetrics]; val != "" {
		enableMetrics = utils.IsTruthy(val)
	}
	return enableMetrics
}

func (h *DaprHandler) getMetricsPort(wrapper ObjectWrapper) int {
	annotationsMap := wrapper.GetTemplateAnnotations()
	metricsPort := defaultMetricsPort
	if val := annotationsMap[annotations.KeyMetricsPort]; val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			metricsPort = v
		}
	}
	return metricsPort
}
