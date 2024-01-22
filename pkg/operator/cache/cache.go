package cache

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatormeta "github.com/dapr/dapr/pkg/operator/meta"
)

var (
	// create pseudo-unique name for empty resources
	randomName    = "dapr-dev-null" + rand.String(20)
	deployDevNull = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: randomName, Namespace: randomName},
	}
	stsDevNull = &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: randomName, Namespace: randomName},
	}

	podDevNull = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: randomName, Namespace: randomName},
	}

	podEmptyStatus    = corev1.PodStatus{}
	podEmptySpec      = corev1.PodSpec{}
	deployEmptyStatus = appsv1.DeploymentStatus{}
	stsEmptyStatus    = appsv1.StatefulSetStatus{}
)

// GetFilteredCache creates a cache that slims down resources to the minimum that is needed for processing and also acts
// as a sinkhole for deployment/statefulsets/pods that are not going to be processed but that cannot be filtered by labels
// to limit what items end up in the cache. The following is removed/clear from the resources:
// - pods -> managed fields, status (we care for spec to find out containers, the rests are set to empty)
// - deploy/sts -> template.spec, status, managedfields (we only care about template/metadata except for injector deployment)
func GetFilteredCache(namespace string, podSelector labels.Selector) cache.NewCacheFunc {
	return func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
		// The only pods we are interested are in watchdog. we don't need to
		// list/watch pods that we are almost sure have dapr sidecar already.
		opts.ByObject = getTransformerFunctions(podSelector)
		if len(namespace) > 0 {
			opts.DefaultNamespaces = map[string]cache.Config{
				namespace: {},
			}
		}
		return cache.New(config, opts)
	}
}

// getTransformerFunctions creates transformers that are called by the DeltaFifo before they are inserted in the cache
// the transformations here try to reduce size of objects, and for some others objects that we don't care about (non-dapr)
// we set all these objects to store a single one, a sort of sinkhole
func getTransformerFunctions(podSelector labels.Selector) map[client.Object]cache.ByObject {
	return map[client.Object]cache.ByObject{
		&corev1.Pod{}: {
			Label: podSelector,
			Transform: func(i any) (any, error) {
				obj, ok := i.(*corev1.Pod)
				if !ok { // probably deletedfinalstateunknown
					return i, nil
				}

				if operatormeta.IsAnnotatedForDapr(obj.ObjectMeta.GetAnnotations()) && !operatormeta.IsSidecarPresent(obj.ObjectMeta.GetLabels()) {
					objClone := obj.DeepCopy()
					objClone.ObjectMeta.ManagedFields = []metav1.ManagedFieldsEntry{}
					objClone.Status = podEmptyStatus
					return objClone, nil
				}

				return podDevNull, nil
			},
		},
		&appsv1.Deployment{}: {
			Transform: func(i any) (any, error) {
				obj, ok := i.(*appsv1.Deployment)
				if !ok {
					return i, nil
				}

				// store injector deployment as is
				if obj.GetLabels()["app"] == operatormeta.SidecarInjectorDeploymentName {
					return i, nil
				}

				// slim down dapr deployments and sinkhole non-dapr ones
				if operatormeta.IsAnnotatedForDapr(obj.Spec.Template.ObjectMeta.GetAnnotations()) {
					// keep metadata but remove the rest
					objClone := obj.DeepCopy()
					objClone.ObjectMeta.ManagedFields = []metav1.ManagedFieldsEntry{}
					objClone.Spec.Template.Spec = podEmptySpec
					objClone.Status = deployEmptyStatus
					return objClone, nil
				}

				return deployDevNull, nil
			},
		},
		&appsv1.StatefulSet{}: {
			Transform: func(i any) (any, error) {
				obj, ok := i.(*appsv1.StatefulSet)
				if !ok {
					return i, nil
				}

				if operatormeta.IsAnnotatedForDapr(obj.Spec.Template.ObjectMeta.GetAnnotations()) {
					// keep metadata but remove the rest
					objClone := obj.DeepCopy()
					objClone.ObjectMeta.ManagedFields = []metav1.ManagedFieldsEntry{}
					objClone.Spec.Template.Spec = podEmptySpec
					objClone.Status = stsEmptyStatus
					return objClone, nil
				}

				return stsDevNull, nil
			},
		},
	}
}
