package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	deployment.Spec.Selector = &meta_v1.LabelSelector{
		MatchLabels: map[string]string{},
	}
	deployment.Spec.Template.Annotations[daprMetricsPortKey] = "12345"

	service := testDaprHandler.createDaprServiceValues(ctx, myDaprService, deployment, "test")
	require.NotNil(t, service)
	assert.Equal(t, "test", service.ObjectMeta.Annotations[appIDAnnotationKey])
	assert.Equal(t, "true", service.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "12345", service.ObjectMeta.Annotations["prometheus.io/port"])
	assert.Equal(t, "/", service.ObjectMeta.Annotations["prometheus.io/path"])

	deployment.Spec.Template.Annotations[daprEnableMetricsKey] = "false"

	service = testDaprHandler.createDaprServiceValues(ctx, myDaprService, deployment, "test")
	require.NotNil(t, service)
	assert.Equal(t, "test", service.ObjectMeta.Annotations[appIDAnnotationKey])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/scrape"])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/port"])
	assert.Equal(t, "", service.ObjectMeta.Annotations["prometheus.io/path"])
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

func getDeploymentWithMetricsPortAnnotation(daprID string, daprEnabled string, metricsPort string) *appsv1.Deployment {
	d := getDeployment(daprID, daprEnabled)
	d.Spec.Template.ObjectMeta.Annotations[daprMetricsPortKey] = metricsPort
	return d
}

func getDeployment(appID string, daprEnabled string) *appsv1.Deployment {
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

	deployment := &appsv1.Deployment{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "app",
		},

		Spec: appsv1.DeploymentSpec{
			Template: podTemplateSpec,
		},
	}

	return deployment
}

func getTestDaprHandler() *DaprHandler {
	return &DaprHandler{}
}
