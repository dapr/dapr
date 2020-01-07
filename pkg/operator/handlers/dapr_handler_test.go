package handlers

import (
	"testing"

	versioned "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/kubernetes"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake "k8s.io/client-go/kubernetes/fake"
)

func TestNewDaprHandler(t *testing.T) {
	d := getTestDaprHandler()
	assert.True(t, d != nil)
}

func TestGetDaprID(t *testing.T) {
	testDaprHandler := getTestDaprHandler()
	t.Run("WithValidId", func(t *testing.T) {
		// Arrange
		expected := "test_id"
		deployment := getDeployment(expected, "true")

		// Act
		got := testDaprHandler.getDaprID(deployment)

		// Assert
		assert.Equal(t, expected, got)
	})

	t.Run("WithEmptyId", func(t *testing.T) {
		// Arrange
		expected := ""
		deployment := getDeployment(expected, "true")

		// Act
		got := testDaprHandler.getDaprID(deployment)

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

func getDeployment(daprID string, daprEnabled string) *appsv1.Deployment {
	// Arrange
	metadata := meta_v1.ObjectMeta{
		Name:   "app",
		Labels: map[string]string{"app": "test_app"},
		Annotations: map[string]string{
			daprIDAnnotationKey:      daprID,
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

func getTestDaprHandler() *DaprHandler {
	fakeClient := fake.NewSimpleClientset()
	kubeAPI := kubernetes.NewAPI(fakeClient, versioned.New(nil))
	return NewDaprHandler(kubeAPI)
}
