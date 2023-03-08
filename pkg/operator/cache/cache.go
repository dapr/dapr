package cache

import (
	v1 "k8s.io/api/apps/v1"
	v13 "k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	operatormeta "github.com/dapr/dapr/pkg/operator/meta"
)

var (
	// create pseudo-unique name for empty resources
	randomName    = "dapr-dev-null" + rand.String(20)
	deployDevNull = &v1.Deployment{
		ObjectMeta: v12.ObjectMeta{Name: randomName, Namespace: randomName},
	}
	stsDevNull = &v1.StatefulSet{
		ObjectMeta: v12.ObjectMeta{Name: randomName, Namespace: randomName},
	}

	podDevNull = &v13.Pod{
		ObjectMeta: v12.ObjectMeta{Name: randomName, Namespace: randomName},
	}

	podEmptyStatus    = v13.PodStatus{}
	podEmptySpec      = v13.PodSpec{}
	deployEmptyStatus = v1.DeploymentStatus{}
	stsEmptyStatus    = v1.StatefulSetStatus{}
)

// GetFilteredCache creates a cache that slims down resources to the minimum that is needed for processing and also acts
// as a sinkhole for deployment/statefulsets/pods that are not going to be processed but that cannot be filtered by labels
// to limit what items end up in the cache. The following is removed/clear from the resources:
// - pods -> managed fields, status (we care for spec to find out containers, the rests are set to empty)
// - deploy/sts -> template.spec, status, managedfields (we only care about template/metadata except for injector deployment)
func GetFilteredCache(podSelector labels.Selector) cache.NewCacheFunc {
	cacheOptions := cache.Options{
		TransformByObject: getTransformerFunctions(),
	}
	if podSelector != nil {
		cacheOptions.SelectorsByObject = // The only pods we are interested are in watchdog. we don't need to list/watch pods that we are almost sure have dapr sidecar already
			cache.SelectorsByObject{
				&v13.Pod{}: {
					Label: podSelector,
				},
			}
	}
	return cache.BuilderWithOptions(cacheOptions)
}

// getTransformerFunctions creates transformers that are called by the DeltaFifo before they are inserted in the cache
// the transformations here try to reduce size of objects, and for some others objects that we don't care about (non-dapr)
// we set all these objects to store a single one, a sort of sinkhole
func getTransformerFunctions() cache.TransformByObject {
	return cache.TransformByObject{
		&v13.Pod{}: func(i interface{}) (interface{}, error) {
			obj, ok := i.(*v13.Pod)
			if !ok { // probably deletedfinalstateunknown
				return i, nil
			}

			if operatormeta.IsAnnotatedForDapr(obj.ObjectMeta.GetAnnotations()) && !operatormeta.IsSidecarPresent(obj.ObjectMeta.GetLabels()) {
				objClone := obj.DeepCopy()
				objClone.ObjectMeta.ManagedFields = []v12.ManagedFieldsEntry{}
				objClone.Status = podEmptyStatus
				return objClone, nil
			} else {
				return podDevNull, nil
			}
		},
		&v1.Deployment{}: func(i interface{}) (interface{}, error) {
			obj, ok := i.(*v1.Deployment)
			if !ok {
				return i, nil
			}
			// store injector deployment as is
			if v, ok := obj.GetLabels()["app"]; ok && v == operatormeta.SidecarInjectorDeploymentName {
				return i, nil
			}

			// slim down dapr deployments and sinkhole non-dapr ones
			if operatormeta.IsAnnotatedForDapr(obj.Spec.Template.ObjectMeta.GetAnnotations()) {
				// keep metadata but remove the rest
				objClone := obj.DeepCopy()
				objClone.ObjectMeta.ManagedFields = []v12.ManagedFieldsEntry{}
				objClone.Spec.Template.Spec = podEmptySpec
				objClone.Status = deployEmptyStatus
				return objClone, nil
			} else {
				return deployDevNull, nil
			}
		},
		&v1.StatefulSet{}: func(i interface{}) (interface{}, error) {
			obj, ok := i.(*v1.StatefulSet)
			if !ok {
				return i, nil
			}
			if operatormeta.IsAnnotatedForDapr(obj.Spec.Template.ObjectMeta.GetAnnotations()) {
				// keep metadata but remove the rest
				objClone := obj.DeepCopy()
				objClone.ObjectMeta.ManagedFields = []v12.ManagedFieldsEntry{}
				objClone.Spec.Template.Spec = podEmptySpec
				objClone.Status = stsEmptyStatus
				return objClone, nil
			} else {
				return stsDevNull, nil
			}
		},
	}
}
