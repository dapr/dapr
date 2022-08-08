package handlers

import (
	"context"
	"reflect"
	"testing"

	"k8s.io/client-go/kubernetes/scheme"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dapr_testing "github.com/dapr/dapr/pkg/testing"
)

func TestNewDaprHandler(t *testing.T) {
	d := getTestDaprHandler()
	assert.True(t, d != nil)
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
		assert.Error(t, err)
	})

	t.Run("invalid char app id", func(t *testing.T) {
		d := getDeployment("myapp@", "true")
		err := getTestDaprHandler().ensureDaprServicePresent(context.TODO(), "default", d)
		assert.Error(t, err)
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
	deployment.GetTemplateAnnotations()[daprMetricsPortKey] = "12345"

	service := testDaprHandler.createDaprServiceValues(ctx, myDaprService, deployment, "test")
	require.NotNil(t, service)
	assert.Equal(t, "test", service.ObjectMeta.Annotations[appIDAnnotationKey])
	assert.Equal(t, "true", service.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "12345", service.ObjectMeta.Annotations["prometheus.io/port"])
	assert.Equal(t, "/", service.ObjectMeta.Annotations["prometheus.io/path"])

	deployment.GetTemplateAnnotations()[daprEnableMetricsKey] = "false"

	service = testDaprHandler.createDaprServiceValues(ctx, myDaprService, deployment, "test")
	require.NotNil(t, service)
	assert.Equal(t, "test", service.ObjectMeta.Annotations[appIDAnnotationKey])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/port"])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/path"])
}

func TestPatchDaprService(t *testing.T) {
	testDaprHandler := getTestDaprHandler()

	s := runtime.NewScheme()
	err := scheme.AddToScheme(s)
	assert.NoError(t, err)
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
	assert.NoError(t, err)
	var actualService corev1.Service
	err = cli.Get(ctx, myDaprService, &actualService)
	assert.NoError(t, err)
	assert.Equal(t, "test", actualService.ObjectMeta.Annotations[appIDAnnotationKey])
	assert.Equal(t, "true", actualService.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "/", actualService.ObjectMeta.Annotations["prometheus.io/path"])
	assert.Len(t, actualService.OwnerReferences, 1)
	assert.Equal(t, "Deployment", actualService.OwnerReferences[0].Kind)
	assert.Equal(t, "app", actualService.OwnerReferences[0].Name)

	err = testDaprHandler.patchDaprService(ctx, myDaprService, deployment, actualService)
	assert.NoError(t, err)
	err = cli.Get(ctx, myDaprService, &actualService)
	assert.NoError(t, err)
	assert.Equal(t, "test", actualService.ObjectMeta.Annotations[appIDAnnotationKey])
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

	t.Run("get match label from wrapper", func(t *testing.T) {
		assert.Equal(t, "test", deploymentWrapper.GetMatchLabels()["app"])
		assert.Equal(t, "test", statefulsetWrapper.GetMatchLabels()["app"])
	})

	t.Run("get annotations from wrapper", func(t *testing.T) {
		assert.Equal(t, "test_id", deploymentWrapper.GetTemplateAnnotations()[appIDAnnotationKey])
		assert.Equal(t, "test_id", statefulsetWrapper.GetTemplateAnnotations()[appIDAnnotationKey])
	})

	t.Run("get object from wrapper", func(t *testing.T) {
		assert.Equal(t, reflect.TypeOf(deploymentWrapper.GetObject()), reflect.TypeOf(&appsv1.Deployment{}))
		assert.Equal(t, reflect.TypeOf(statefulsetWrapper.GetObject()), reflect.TypeOf(&appsv1.StatefulSet{}))
		assert.NotEqual(t, reflect.TypeOf(statefulsetWrapper.GetObject()), reflect.TypeOf(&appsv1.Deployment{}))
		assert.NotEqual(t, reflect.TypeOf(deploymentWrapper.GetObject()), reflect.TypeOf(&appsv1.StatefulSet{}))
	})
}

func TestInit(t *testing.T) {
	mgr := dapr_testing.NewMockManager()

	_ = scheme.AddToScheme(mgr.GetScheme())

	handler := NewDaprHandler(mgr)

	t.Run("test init dapr handler", func(t *testing.T) {
		assert.NotNil(t, handler)

		err := handler.Init()

		assert.Nil(t, err)

		assert.Equal(t, 2, len(mgr.GetRunnables()))

		srv := &corev1.Service{}
		val := mgr.GetIndexerFunc(&corev1.Service{})(srv)
		assert.Nil(t, val)

		trueA := true
		srv = &corev1.Service{
			ObjectMeta: meta_v1.ObjectMeta{
				OwnerReferences: []meta_v1.OwnerReference{
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

		// the runnable is sigs.k8s.io/controller-runtime/pkg/internal/controller.Controller
		reconciler := reflect.Indirect(reflect.ValueOf(deploymentCtl)).FieldByName("Do").Interface().(*Reconciler)

		wrapper := reconciler.newWrapper()

		assert.NotNil(t, wrapper)

		assert.Equal(t, reflect.TypeOf(&appsv1.Deployment{}), reflect.TypeOf(wrapper.GetObject()))

		reconciler = reflect.Indirect(reflect.ValueOf(statefulsetCtl)).FieldByName("Do").Interface().(*Reconciler)

		wrapper = reconciler.newWrapper()

		assert.NotNil(t, wrapper)

		assert.Equal(t, reflect.TypeOf(&appsv1.StatefulSet{}), reflect.TypeOf(wrapper.GetObject()))
	})
}

func getDeploymentWithMetricsPortAnnotation(daprID string, daprEnabled string, metricsPort string) ObjectWrapper {
	d := getDeployment(daprID, daprEnabled)
	d.GetTemplateAnnotations()[daprMetricsPortKey] = metricsPort
	return d
}

func getDeployment(appID string, daprEnabled string) ObjectWrapper {
	// Arrange
	metadata := meta_v1.ObjectMeta{
		Name:   "app",
		Labels: map[string]string{"app": "test_app"},
		Annotations: map[string]string{
			appIDAnnotationKey:       appID,
			daprEnabledAnnotationKey: daprEnabled,
			daprEnableMetricsKey:     "true",
		},
	}

	podTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: metadata,
	}

	deployment := appsv1.Deployment{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "app",
		},

		Spec: appsv1.DeploymentSpec{
			Template: podTemplateSpec,
			Selector: &meta_v1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
		},
	}

	return &DeploymentWrapper{deployment}
}

func getStatefulSet(appID string, daprEnabled string) ObjectWrapper {
	// Arrange
	metadata := meta_v1.ObjectMeta{
		Name:   "app",
		Labels: map[string]string{"app": "test_app"},
		Annotations: map[string]string{
			appIDAnnotationKey:       appID,
			daprEnabledAnnotationKey: daprEnabled,
			daprEnableMetricsKey:     "true",
		},
	}

	podTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: metadata,
	}

	statefulset := appsv1.StatefulSet{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "app",
		},

		Spec: appsv1.StatefulSetSpec{
			Template: podTemplateSpec,
			Selector: &meta_v1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
		},
	}

	return &StatefulSetWrapper{
		statefulset,
	}
}

func getTestDaprHandler() *DaprHandler {
	return &DaprHandler{}
}
