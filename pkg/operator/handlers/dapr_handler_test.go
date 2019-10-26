package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	testclient "github.com/dapr/dapr/pkg/client/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetDaprID(t *testing.T) {

	t.Run("WithValidId", func(t *testing.T) {
		// Arrange
		expected := "test_id"
		deployment := getDeployment(expected, "true")
		testDaprHandler := NewDaprHandler(testclient.New(nil))

		// Act
		got := testDaprHandler.getDaprID(deployment)

		// Assert
		assert.Equal(t, expected, got)
	})

	t.Run("WithEmptyId", func(t *testing.T) {
		// Arrange
		expected := ""
		deployment := getDeployment(expected, "true")
		testDaprHandler := NewDaprHandler(testclient.New(nil))

		// Act
		got := testDaprHandler.getDaprID(deployment)

		// Assert
		assert.Equal(t, expected, got)
	})
}

func TestIsAnnotatedForDapr(t *testing.T) {

	t.Run("Enabled", func(t *testing.T) {
		// Arrange
		expected := "true"
		deployment := getDeployment("test_id", expected)
		testDaprHandler := NewDaprHandler(testclient.New(nil))

		// Act
		got := testDaprHandler.isAnnotatedForDapr(deployment)

		// Assert
		assert.True(t, got)
	})

	t.Run("Disabled", func(t *testing.T) {
		// Arrange
		expected := "false"
		deployment := getDeployment("test_id", expected)
		testDaprHandler := NewDaprHandler(testclient.New(nil))

		// Act
		got := testDaprHandler.isAnnotatedForDapr(deployment)

		// Assert
		assert.False(t, got)

	})

	t.Run("Invalid", func(t *testing.T) {
		// Arrange
		expected := "0"
		deployment := getDeployment("test_id", expected)
		testDaprHandler := NewDaprHandler(testclient.New(nil))

		// Act
		got := testDaprHandler.isAnnotatedForDapr(deployment)

		// Assert
		assert.False(t, got)
	})
}

func getDeployment(daprId string, daprEnabled string) *appsv1.Deployment {

	// Arrange
	metadata := meta_v1.ObjectMeta{
		Name:   "app",
		Labels: map[string]string{"app": "test_app"},
		Annotations: map[string]string{
			daprIDAnnotationKey:      daprId,
			daprEnabledAnnotationKey: daprEnabled,
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
