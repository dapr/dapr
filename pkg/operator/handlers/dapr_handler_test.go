package handlers

import (
	"context"
	"reflect"
	"testing"

	argov1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/operator/testobjects"
	dapr_testing "github.com/dapr/dapr/pkg/testing"
)

func TestNewDaprHandler(t *testing.T) {
	d := getTestDaprHandler()
	assert.NotNil(t, d)
}

func TestGetAppID(t *testing.T) {
	testDaprHandler := getTestDaprHandler()
	t.Run("WithValidId", func(t *testing.T) {
		// Arrange
		expected := "test_id"
		deployment := getDeployment(expected, "true")

		// Act
		got := testDaprHandler.getAppID(deployment)

		// Assert
		assert.Equal(t, expected, got)
	})

	t.Run("WithEmptyId", func(t *testing.T) {
		// Arrange
		expected := ""
		deployment := getDeployment(expected, "true")

		// Act
		got := testDaprHandler.getAppID(deployment)

		// Assert
		assert.Equal(t, expected, got)
	})
}

func TestIsAnnotatedForDapr(t *testing.T) {
	testDaprHandler := getTestDaprHandler()
	t.Run("Enabled", func(t *testing.T) {
		// Arrange
		expected := "true"
		deployment := getDeployment("test_id", expected)

		// Act
		got := testDaprHandler.isAnnotatedForDapr(deployment)

		// Assert
		assert.True(t, got)
	})

	t.Run("Disabled", func(t *testing.T) {
		// Arrange
		expected := "false"
		deployment := getDeployment("test_id", expected)

		// Act
		got := testDaprHandler.isAnnotatedForDapr(deployment)

		// Assert
		assert.False(t, got)
	})

	t.Run("Invalid", func(t *testing.T) {
		// Arrange
		expected := "0"
		deployment := getDeployment("test_id", expected)

		// Act
		got := testDaprHandler.isAnnotatedForDapr(deployment)

		// Assert
		assert.False(t, got)
	})
}

func TestDaprService(t *testing.T) {
	t.Run("invalid empty app id", func(t *testing.T) {
		d := getDeployment("", "true")
		err := getTestDaprHandler().ensureDaprServicePresent(context.TODO(), "default", d)
		require.Error(t, err)
	})

	t.Run("invalid char app id", func(t *testing.T) {
		d := getDeployment("myapp@", "true")
		err := getTestDaprHandler().ensureDaprServicePresent(context.TODO(), "default", d)
		require.Error(t, err)
	})
}

func TestCreateDaprServiceAppIDAndMetricsSettings(t *testing.T) {
	testDaprHandler := getTestDaprHandler()
	ctx := context.Background()
	myDaprService := types.NamespacedName{
		Namespace: "test",
		Name:      "test",
	}
	deployment := getDeployment("test", "true")
	deployment.GetTemplateAnnotations()[annotations.KeyMetricsPort] = "12345"

	service := testDaprHandler.createDaprServiceValues(ctx, myDaprService, deployment, "test")
	require.NotNil(t, service)
	assert.Equal(t, "test", service.ObjectMeta.Annotations[annotations.KeyAppID])
	assert.Equal(t, "true", service.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "12345", service.ObjectMeta.Annotations["prometheus.io/port"])
	assert.Equal(t, "/", service.ObjectMeta.Annotations["prometheus.io/path"])

	deployment.GetTemplateAnnotations()[annotations.KeyEnableMetrics] = "false"

	service = testDaprHandler.createDaprServiceValues(ctx, myDaprService, deployment, "test")
	require.NotNil(t, service)
	assert.Equal(t, "test", service.ObjectMeta.Annotations[annotations.KeyAppID])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/port"])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/path"])
}

func TestPatchDaprService(t *testing.T) {
	testDaprHandler := getTestDaprHandler()

	s := runtime.NewScheme()
	err := scheme.AddToScheme(s)
	require.NoError(t, err)
	testDaprHandler.Scheme = s

	cli := fake.NewClientBuilder().WithScheme(s).Build()
	testDaprHandler.Client = cli

	ctx := context.Background()
	myDaprService := types.NamespacedName{
		Namespace: "test",
		Name:      "test",
	}
	deployment := getDeployment("test", "true")

	err = testDaprHandler.createDaprService(ctx, myDaprService, deployment)
	require.NoError(t, err)
	var actualService corev1.Service
	err = cli.Get(ctx, myDaprService, &actualService)
	require.NoError(t, err)
	assert.Equal(t, "test", actualService.ObjectMeta.Annotations[annotations.KeyAppID])
	assert.Equal(t, "true", actualService.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "/", actualService.ObjectMeta.Annotations["prometheus.io/path"])
	assert.Len(t, actualService.OwnerReferences, 1)
	assert.Equal(t, "Deployment", actualService.OwnerReferences[0].Kind)
	assert.Equal(t, "app", actualService.OwnerReferences[0].Name)

	err = testDaprHandler.patchDaprService(ctx, myDaprService, deployment, actualService)
	require.NoError(t, err)
	err = cli.Get(ctx, myDaprService, &actualService)
	require.NoError(t, err)
	assert.Equal(t, "test", actualService.ObjectMeta.Annotations[annotations.KeyAppID])
	assert.Equal(t, "true", actualService.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "/", actualService.ObjectMeta.Annotations["prometheus.io/path"])
	assert.Len(t, actualService.OwnerReferences, 1)
	assert.Equal(t, "Deployment", actualService.OwnerReferences[0].Kind)
	assert.Equal(t, "app", actualService.OwnerReferences[0].Name)
}

func TestGetMetricsPort(t *testing.T) {
	testDaprHandler := getTestDaprHandler()
	t.Run("metrics port override", func(t *testing.T) {
		// Arrange
		deployment := getDeploymentWithMetricsPortAnnotation("test_id", "true", "5050")

		// Act
		p := testDaprHandler.getMetricsPort(deployment)

		// Assert
		assert.Equal(t, 5050, p)
	})
	t.Run("invalid metrics port override", func(t *testing.T) {
		// Arrange
		deployment := getDeploymentWithMetricsPortAnnotation("test_id", "true", "abc")

		// Act
		p := testDaprHandler.getMetricsPort(deployment)

		// Assert
		assert.Equal(t, defaultMetricsPort, p)
	})
	t.Run("no metrics port override", func(t *testing.T) {
		// Arrange
		deployment := getDeployment("test_id", "true")

		// Act
		p := testDaprHandler.getMetricsPort(deployment)

		// Assert
		assert.Equal(t, defaultMetricsPort, p)
	})
}

func TestWrapper(t *testing.T) {
	deploymentWrapper := getDeployment("test_id", "true")
	statefulsetWrapper := getStatefulSet("test_id", "true")
	rolloutWrapper := getRollout("test_id", "true")

	t.Run("get match label from wrapper", func(t *testing.T) {
		assert.Equal(t, "test", deploymentWrapper.GetMatchLabels()["app"])
		assert.Equal(t, "test", statefulsetWrapper.GetMatchLabels()["app"])
		assert.Equal(t, "test", rolloutWrapper.GetMatchLabels()["app"])
	})

	t.Run("get annotations from wrapper", func(t *testing.T) {
		assert.Equal(t, "test_id", deploymentWrapper.GetTemplateAnnotations()[annotations.KeyAppID])
		assert.Equal(t, "test_id", statefulsetWrapper.GetTemplateAnnotations()[annotations.KeyAppID])
		assert.Equal(t, "test_id", rolloutWrapper.GetTemplateAnnotations()[annotations.KeyAppID])
	})

	t.Run("get object from wrapper", func(t *testing.T) {
		assert.Equal(t, reflect.TypeOf(deploymentWrapper.GetObject()), reflect.TypeOf(&appsv1.Deployment{}))
		assert.Equal(t, reflect.TypeOf(statefulsetWrapper.GetObject()), reflect.TypeOf(&appsv1.StatefulSet{}))
		assert.Equal(t, reflect.TypeOf(rolloutWrapper.GetObject()), reflect.TypeOf(&argov1alpha1.Rollout{}))
		assert.NotEqual(t, reflect.TypeOf(statefulsetWrapper.GetObject()), reflect.TypeOf(&appsv1.Deployment{}))
		assert.NotEqual(t, reflect.TypeOf(deploymentWrapper.GetObject()), reflect.TypeOf(&appsv1.StatefulSet{}))
		assert.NotEqual(t, reflect.TypeOf(rolloutWrapper.GetObject()), reflect.TypeOf(&appsv1.Deployment{}))
	})
}

func TestInit(t *testing.T) {
	mgr := dapr_testing.NewMockManager()

	_ = scheme.AddToScheme(mgr.GetScheme())
	_ = argov1alpha1.AddToScheme(mgr.GetScheme())

	handler := NewDaprHandlerWithOptions(mgr, &Options{
		ArgoRolloutServiceReconcilerEnabled: true,
	})

	t.Run("test init dapr handler", func(t *testing.T) {
		assert.NotNil(t, handler)

		err := handler.Init(context.Background())

		require.NoError(t, err)

		assert.Len(t, mgr.GetRunnables(), 3)

		srv := &corev1.Service{}
		val := mgr.GetIndexerFunc(&corev1.Service{})(srv)
		assert.Nil(t, val)

		trueA := true
		srv = &corev1.Service{
			ObjectMeta: metaV1.ObjectMeta{
				OwnerReferences: []metaV1.OwnerReference{
					{
						Name:       "TestName",
						Controller: &trueA,
						APIVersion: "apps/v1",
						Kind:       "Deployment",
					},
				},
			},
		}

		val = mgr.GetIndexerFunc(&corev1.Service{})(srv)
		assert.Equal(t, []string{"TestName"}, val)
	})

	t.Run("test wrapper", func(t *testing.T) {
		deploymentCtl := mgr.GetRunnables()[0]
		statefulsetCtl := mgr.GetRunnables()[1]
		rolloutCtl := mgr.GetRunnables()[2]

		// the runnable is sigs.k8s.io/controller-runtime/pkg/internal/controller.Controller
		reconciler := reflect.Indirect(reflect.ValueOf(deploymentCtl)).FieldByName("Do").Interface().(*Reconciler)

		wrapper := reconciler.newWrapper()

		assert.NotNil(t, wrapper)

		assert.Equal(t, reflect.TypeOf(&appsv1.Deployment{}), reflect.TypeOf(wrapper.GetObject()))

		reconciler = reflect.Indirect(reflect.ValueOf(statefulsetCtl)).FieldByName("Do").Interface().(*Reconciler)

		wrapper = reconciler.newWrapper()

		assert.NotNil(t, wrapper)

		assert.Equal(t, reflect.TypeOf(&appsv1.StatefulSet{}), reflect.TypeOf(wrapper.GetObject()))

		reconciler = reflect.Indirect(reflect.ValueOf(rolloutCtl)).FieldByName("Do").Interface().(*Reconciler)

		wrapper = reconciler.newWrapper()

		assert.NotNil(t, wrapper)

		assert.Equal(t, reflect.TypeOf(&argov1alpha1.Rollout{}), reflect.TypeOf(wrapper.GetObject()))
	})
}

func getDeploymentWithMetricsPortAnnotation(daprID string, daprEnabled string, metricsPort string) ObjectWrapper {
	d := getDeployment(daprID, daprEnabled)
	d.GetTemplateAnnotations()[annotations.KeyMetricsPort] = metricsPort
	return d
}

func getDeployment(appID string, daprEnabled string) ObjectWrapper {
	return &DeploymentWrapper{testobjects.GetDeployment(appID, daprEnabled)}
}

func getStatefulSet(appID string, daprEnabled string) ObjectWrapper {
	return &StatefulSetWrapper{testobjects.GetStatefulSet(appID, daprEnabled)}
}

func getRollout(appID string, daprEnabled string) ObjectWrapper {
	metadata := metaV1.ObjectMeta{
		Name:   "app",
		Labels: map[string]string{"app": "test_app"},
		Annotations: map[string]string{
			annotations.KeyAppID:         appID,
			annotations.KeyEnabled:       daprEnabled,
			annotations.KeyEnableMetrics: "true",
		},
	}

	podTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: metadata,
	}

	rollout := argov1alpha1.Rollout{
		ObjectMeta: metaV1.ObjectMeta{
			Name: "app",
		},
		Spec: argov1alpha1.RolloutSpec{
			Template: podTemplateSpec,
			Selector: &metaV1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
		},
	}

	return &RolloutWrapper{
		rollout,
	}
}

func getTestDaprHandler() *DaprHandler {
	return &DaprHandler{}
}
