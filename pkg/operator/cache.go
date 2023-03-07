package operator

import (
	v1 "k8s.io/api/apps/v1"
	v13 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/dapr/dapr/pkg/injector/sidecar"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/utils"
)

// GetFilteredCache creates a cache that slims down resources to the minimum that is needed for processing and also acts
// as a sink hole for deployment/statefulsets/pods that are not going to be processed but that cannot be filtered by labels
// to limit what items end up in the cache. The following is removed/clear from the resources:
// - pods -> managed fields, status
// - deploy/sts -> template.spec, status, managedfields
func GetFilteredCache() cache.NewCacheFunc {
	randomName := "dapr-dev-null" + rand.String(20)
	deployDevNull := &v1.Deployment{
		ObjectMeta: v12.ObjectMeta{Name: randomName, Namespace: randomName},
	}
	stsDevNull := &v1.StatefulSet{
		ObjectMeta: v12.ObjectMeta{Name: randomName, Namespace: randomName},
	}

	isAnnotatedForDapr := func(w map[string]string) bool {
		if v, ok := w[annotations.KeyEnabled]; !ok {
			return false
		} else {
			return utils.IsTruthy(v)
		}
	}

	isSidecarInjected := func(w map[string]string) bool {
		if _, ok := w[sidecar.SidecarInjectedLabel]; ok {
			return true
		}
		if _, ok := w[watchdogPatchedLabel]; ok {
			return true
		}
		return false
	}

	podDevNull := &v13.Pod{
		ObjectMeta: v12.ObjectMeta{Name: randomName, Namespace: randomName},
	}

	podEmptyStatus := v13.PodStatus{}
	podEmptySpec := v13.PodSpec{}
	deployEmptyStatus := v1.DeploymentStatus{}
	stsEmptyStatus := v1.StatefulSetStatus{}

	return cache.BuilderWithOptions(cache.Options{
		SelectorsByObject: cache.SelectorsByObject{
			&v13.Pod{}: {
				Label: getSideCarInjectedNotExistsSelector(),
			},
		},
		TransformByObject: cache.TransformByObject{
			&v13.Pod{}: func(i interface{}) (interface{}, error) {
				obj, ok := i.(runtime.Object)
				if !ok { // probably deletedfinalstateunknown
					return i, nil
				}
				accessor, err := meta.Accessor(obj)
				if err != nil {
					return i, nil
				}
				accessor.SetManagedFields([]v12.ManagedFieldsEntry{})

				switch t := i.(type) {
				case *v13.Pod:
					if isAnnotatedForDapr(t.ObjectMeta.GetAnnotations()) && !isSidecarInjected(t.ObjectMeta.GetLabels()) {
						objClone := t.DeepCopy()
						objClone.Status = podEmptyStatus
						return objClone, nil
					} else {
						return podDevNull, nil
					}
				}
				return i, nil
			},
			&v1.Deployment{}: func(i interface{}) (interface{}, error) {
				obj, ok := i.(*v1.Deployment)
				if !ok {
					return i, nil
				}
				if v, ok := obj.Labels["app"]; ok && v == sidecarInjectorDeploymentName {
					return i, nil
				}
				if isAnnotatedForDapr(obj.Spec.Template.ObjectMeta.GetAnnotations()) {
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
				if isAnnotatedForDapr(obj.Spec.Template.ObjectMeta.GetAnnotations()) {
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
		},
	})
}
