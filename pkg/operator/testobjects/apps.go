package testobjects

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/injector/annotations"
)

type resourceOpts struct {
	name                  string
	namespace             string
	additionalLabels      map[string]string
	additionalAnnotations map[string]string
}

func (r resourceOpts) updateMetadata(m *metaV1.ObjectMeta) {
	if len(r.additionalLabels) != 0 {
		if m.Labels == nil {
			m.Labels = make(map[string]string, len(r.additionalLabels))
		}
		for k, v := range r.additionalLabels {
			m.Labels[k] = v
		}
	}
	if len(r.additionalAnnotations) != 0 {
		if m.Annotations == nil {
			m.Annotations = make(map[string]string, len(r.additionalAnnotations))
		}
		for k, v := range r.additionalAnnotations {
			m.Annotations[k] = v
		}
	}
}

type resourceOptsFunc func(o *resourceOpts)

func NameNamespace(name, namespace string) resourceOptsFunc {
	return func(o *resourceOpts) {
		o.name = name
		o.namespace = namespace
	}
}

func AddLabels(l map[string]string) resourceOptsFunc {
	return func(o *resourceOpts) {
		o.additionalLabels = l
	}
}

func AddAnnotations(a map[string]string) resourceOptsFunc {
	return func(o *resourceOpts) {
		o.additionalAnnotations = a
	}
}

func getPodMetadata(appID string, daprEnabled string) metaV1.ObjectMeta {
	return metaV1.ObjectMeta{
		Name:   "app",
		Labels: map[string]string{"app": "test_app"},
		Annotations: map[string]string{
			annotations.KeyAppID:         appID,
			annotations.KeyEnabled:       daprEnabled,
			annotations.KeyEnableMetrics: "true",
		},
	}
}

func GetPod(appID string, daprEnabled string, withOpts ...resourceOptsFunc) corev1.Pod {
	// Arrange
	pod := corev1.Pod{
		ObjectMeta: getPodMetadata(appID, daprEnabled),
	}
	opts := getOptsOrDefaults(withOpts)
	opts.updateMetadata(&pod.ObjectMeta)
	pod.Name = opts.name
	pod.Namespace = opts.namespace
	return pod
}

func getOptsOrDefaults(withOpts []resourceOptsFunc) resourceOpts {
	opts := resourceOpts{
		name:      "app",
		namespace: "test",
	}
	for _, o := range withOpts {
		o(&opts)
	}
	return opts
}

func GetDeployment(appID string, daprEnabled string, withOpts ...resourceOptsFunc) appsv1.Deployment {
	podTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: getPodMetadata(appID, daprEnabled),
	}

	opts := getOptsOrDefaults(withOpts)

	deployment := appsv1.Deployment{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      opts.name,
			Namespace: opts.namespace,
		},

		Spec: appsv1.DeploymentSpec{
			Template: podTemplateSpec,
			Selector: &metaV1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
		},
	}
	opts.updateMetadata(&deployment.ObjectMeta)

	return deployment
}

func GetStatefulSet(appID string, daprEnabled string, withOpts ...resourceOptsFunc) appsv1.StatefulSet {
	podTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: getPodMetadata(appID, daprEnabled),
	}

	opts := getOptsOrDefaults(withOpts)

	statefulset := appsv1.StatefulSet{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      opts.name,
			Namespace: opts.namespace,
		},

		Spec: appsv1.StatefulSetSpec{
			Template: podTemplateSpec,
			Selector: &metaV1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
		},
	}
	opts.updateMetadata(&statefulset.ObjectMeta)

	return statefulset
}
