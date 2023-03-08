package cache

import (
	"testing"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatormeta "github.com/dapr/dapr/pkg/operator/meta"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/dapr/dapr/pkg/injector/sidecar"

	"github.com/dapr/dapr/pkg/operator/testobjects"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	cache2 "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// convertToByGVK exposed/modified from sigs.k8s.io/controller-runtime/pkg/cache/cache.go:427
func convertToByGVK[T any](byObject map[client.Object]T) (map[schema.GroupVersionKind]T, error) {
	byGVK := map[schema.GroupVersionKind]T{}
	for object, value := range byObject {
		gvk, err := apiutil.GVKForObject(object, scheme.Scheme)
		if err != nil {
			return nil, err
		}
		byGVK[gvk] = value
	}
	return byGVK, nil
}

func getObjectTransformer(t *testing.T, o client.Object) cache2.TransformFunc {
	transformers := getTransformerFunctions()
	transformerByGVK, err := convertToByGVK[cache2.TransformFunc](transformers)
	require.NoError(t, err)

	gvk, err := apiutil.GVKForObject(o, scheme.Scheme)
	require.NoError(t, err)

	podTransformer, ok := transformerByGVK[gvk]
	require.True(t, ok)
	return podTransformer
}

func getNewTestStore() cache2.Store {
	return cache2.NewStore(func(obj interface{}) (string, error) {
		o := obj.(client.Object)
		return o.GetNamespace() + "/" + o.GetName(), nil
	})
}

func Test_podTransformer(t *testing.T) {
	podTransformer := getObjectTransformer(t, &corev1.Pod{})

	t.Run("allDapr", func(t *testing.T) {
		store := getNewTestStore()

		pods := []corev1.Pod{
			testobjects.GetPod("test", "true", testobjects.NameNamespace("pod1", "ns1")),
			testobjects.GetPod("test", "true", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetPod("test", "true", testobjects.NameNamespace("pod3", "ns3")),
		}

		for _, p := range pods {
			p := p
			obj, err := podTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), len(pods))
	})

	t.Run("noDaprPodsShouldCoalesceAllToOne", func(t *testing.T) {
		store := getNewTestStore()

		pods := []corev1.Pod{
			testobjects.GetPod("test", "no", testobjects.NameNamespace("pod1", "ns1")),
			testobjects.GetPod("test", "no", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetPod("test", "no", testobjects.NameNamespace("pod3", "ns3")),
		}

		for _, p := range pods {
			p := p
			obj, err := podTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), 1)
	})

	t.Run("someInjectedPodsShouldBeCoalesced", func(t *testing.T) {
		store := getNewTestStore()

		pods := []corev1.Pod{
			testobjects.GetPod("test", "true", testobjects.NameNamespace("pod1", "ns1"),
				testobjects.AddLabels(map[string]string{sidecar.SidecarInjectedLabel: "true"})),
			testobjects.GetPod("test", "true", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetPod("test", "no", testobjects.NameNamespace("pod3", "ns3")),
		}

		for _, p := range pods {
			p := p // gosec
			obj, err := podTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), 2)
	})
}

func Test_deployTransformer(t *testing.T) {
	deployTransformer := getObjectTransformer(t, &appsv1.Deployment{})

	t.Run("allDapr", func(t *testing.T) {
		store := getNewTestStore()

		deployments := []appsv1.Deployment{
			testobjects.GetDeployment("test", "true", testobjects.NameNamespace("pod1", "ns1")),
			testobjects.GetDeployment("test", "true", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetDeployment("test", "true", testobjects.NameNamespace("pod3", "ns3")),
		}

		for _, p := range deployments {
			p := p
			obj, err := deployTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), len(deployments))

		depObj, ok, err := store.Get(&appsv1.Deployment{ObjectMeta: v1.ObjectMeta{Name: "pod1", Namespace: "ns1"}})
		require.NoError(t, err)
		require.True(t, ok)
		dep := depObj.(*appsv1.Deployment)
		require.Equal(t, dep.Status, deployEmptyStatus)
		require.Equal(t, dep.Spec.Template.Spec, podEmptySpec)
	})
	t.Run("allNonDapr", func(t *testing.T) {
		store := getNewTestStore()

		deployments := []appsv1.Deployment{
			testobjects.GetDeployment("test", "false", testobjects.NameNamespace("pod1", "ns1")),
			testobjects.GetDeployment("test", "false", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetDeployment("test", "false", testobjects.NameNamespace("pod3", "ns3")),
		}

		for _, p := range deployments {
			p := p
			obj, err := deployTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), 1)
	})
	t.Run("allNonDaprPlusInjectorDeployment", func(t *testing.T) {
		store := getNewTestStore()

		deployments := []appsv1.Deployment{
			testobjects.GetDeployment("test", "false", testobjects.NameNamespace("pod1", "ns1")),
			testobjects.GetDeployment("test", "false", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetDeployment("test", "false", testobjects.NameNamespace("pod3", "ns3")),
			testobjects.GetDeployment("test", "false", testobjects.NameNamespace(operatormeta.SidecarInjectorDeploymentName, "dapr-system"),
				testobjects.AddLabels(map[string]string{"app": operatormeta.SidecarInjectorDeploymentName})),
		}

		for _, p := range deployments {
			p := p
			obj, err := deployTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), 2)
	})
}

func Test_stsTransformer(t *testing.T) {
	stsTransformer := getObjectTransformer(t, &appsv1.StatefulSet{})

	t.Run("allDapr", func(t *testing.T) {
		store := getNewTestStore()

		statefulsets := []appsv1.StatefulSet{
			testobjects.GetStatefulSet("test", "true", testobjects.NameNamespace("pod1", "ns1")),
			testobjects.GetStatefulSet("test", "true", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetStatefulSet("test", "true", testobjects.NameNamespace("pod3", "ns3")),
		}

		for _, p := range statefulsets {
			p := p
			obj, err := stsTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), len(statefulsets))

		depObj, ok, err := store.Get(&appsv1.StatefulSet{ObjectMeta: v1.ObjectMeta{Name: "pod1", Namespace: "ns1"}})
		require.NoError(t, err)
		require.True(t, ok)
		sts := depObj.(*appsv1.StatefulSet)
		require.Equal(t, sts.Status, stsEmptyStatus)
		require.Equal(t, sts.Spec.Template.Spec, podEmptySpec)
	})
	t.Run("allNonDapr", func(t *testing.T) {
		store := getNewTestStore()

		statefulsets := []appsv1.StatefulSet{
			testobjects.GetStatefulSet("test", "false", testobjects.NameNamespace("pod1", "ns1")),
			testobjects.GetStatefulSet("test", "false", testobjects.NameNamespace("pod2", "ns2")),
			testobjects.GetStatefulSet("test", "false", testobjects.NameNamespace("pod3", "ns3")),
		}

		for _, p := range statefulsets {
			p := p
			obj, err := stsTransformer(&p)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}
		require.Len(t, store.List(), 1)
	})
}
