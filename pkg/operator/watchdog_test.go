package operator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/ratelimit"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/injector/sidecar"

	"github.com/dapr/dapr/pkg/injector/annotations"

	corev1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func createMockInjectorDeployment(replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "injector",
			Namespace: "dapr-system",
			Labels:    map[string]string{"app": sidecarInjectorDeploymentName},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(replicas),
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas: replicas,
		},
	}
}

func createMockPods(n, daprized, injected, daprdPresent int) (pods []*corev1.Pod) {
	pods = make([]*corev1.Pod, n)
	for i := 0; i < n; i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("pod-%d", i),
				Namespace:   "default",
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "my-app", Image: "quay.io/prometheus/busybox-linux-arm64", Args: []string{"sh", "-c", "sleep 3600"}}},
			},
			Status: corev1.PodStatus{},
		}
		if i < daprized {
			pods[i].Annotations[annotations.KeyEnabled] = "true"
		}
		if i < injected {
			pods[i].Labels[sidecar.SidecarInjectedLabel] = "true"
		}
		if i < daprdPresent {
			pods[i].Spec.Containers = append(pods[i].Spec.Containers, corev1.Container{
				Name:  sidecarContainerName,
				Image: "quay.io/prometheus/busybox-linux-arm64", Args: []string{"sh", "-c", "sleep 3600"},
			},
			)
		}
	}
	return pods
}

func TestDaprWatchdog_listPods(t *testing.T) {
	ctx := context.Background()
	rl := ratelimit.NewUnlimited()

	t.Run("injectorNotPresent", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects().Build()
		dw := &DaprWatchdog{client: ctlClient}
		require.False(t, dw.listPods(ctx, nil))
	})

	t.Run("injectorPresentNoReplicas", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(0)).Build()
		dw := &DaprWatchdog{client: ctlClient}
		require.False(t, dw.listPods(ctx, nil))
	})

	t.Run("noPods", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(1)).Build()
		dw := &DaprWatchdog{client: ctlClient}
		require.True(t, dw.listPods(ctx, getSideCarInjectedNotExistsSelector()))
	})

	t.Run("noPodsWithAnnotations", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(1)).Build()
		dw := &DaprWatchdog{client: ctlClient}
		pods := createMockPods(10, 0, 0, 0)
		for _, pod := range pods {
			require.NoError(t, ctlClient.Create(ctx, pod))
		}
		require.True(t, dw.listPods(ctx, getSideCarInjectedNotExistsSelector()))
		t.Log("all pods should be present")
		for _, pod := range pods {
			require.NoError(t, ctlClient.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{}))
		}
	})
	t.Run("noInjectedPods", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(1)).Build()
		dw := &DaprWatchdog{client: ctlClient, restartLimiter: rl}
		daprized := 5
		var injected, running int
		pods := createMockPods(10, daprized, injected, running)
		for _, pod := range pods {
			require.NoError(t, ctlClient.Create(ctx, pod))
		}
		require.True(t, dw.listPods(ctx, getSideCarInjectedNotExistsSelector()))
		t.Log("daprized pods should be deleted")
		assertExpectedPodsDeleted(t, pods, ctlClient, ctx, daprized, running, injected)
	})
	t.Run("noInjectedPodsSomeRunning", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(1)).Build()
		dw := &DaprWatchdog{client: ctlClient, restartLimiter: rl}
		daprized := 5
		running := 2
		var injected int
		pods := createMockPods(10, daprized, injected, running)
		for _, pod := range pods {
			require.NoError(t, ctlClient.Create(ctx, pod))
		}
		require.True(t, dw.listPods(ctx, getSideCarInjectedNotExistsSelector()))
		t.Log("daprized pods should be deleted except those running")
		assertExpectedPodsDeleted(t, pods, ctlClient, ctx, daprized, running, injected)
	})
	t.Run("someInjectedPodsWatchdogCannotPatch", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(1)).Build()
		dw := &DaprWatchdog{client: ctlClient, restartLimiter: rl}
		daprized := 5
		running := 1
		injected := 3
		pods := createMockPods(10, daprized, injected, running)
		for _, pod := range pods {
			require.NoError(t, ctlClient.Create(ctx, pod))
		}
		require.True(t, dw.listPods(ctx, getSideCarInjectedNotExistsSelector()))
		t.Log("daprized pods should be deleted except those running")
		assertExpectedPodsDeleted(t, pods, ctlClient, ctx, daprized, running, injected)
		assertExpectedPodsPatched(t, ctlClient, ctx, 0) // not expecting any patched pods as all pods with sidecar already have the injected label
	})
	t.Run("someInjectedPodsWatchdogCanPatch", func(t *testing.T) {
		ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(1)).Build()
		dw := &DaprWatchdog{client: ctlClient, restartLimiter: rl, canPatchPodLabels: true}
		daprized := 5
		running := 3
		injected := 1
		pods := createMockPods(10, daprized, injected, running)
		for _, pod := range pods {
			require.NoError(t, ctlClient.Create(ctx, pod))
		}
		require.True(t, dw.listPods(ctx, getSideCarInjectedNotExistsSelector()))
		t.Log("daprized pods should be deleted except those running")
		assertExpectedPodsDeleted(t, pods, ctlClient, ctx, daprized, running, injected)
		assertExpectedPodsPatched(t, ctlClient, ctx, 2) // expecting 2, as we have 3 with sidecar but only one with label injected
	})
}

// assertExpectedPodsPatched check that we have patched the pods that did not have the label when the watchdog can patch pods
func assertExpectedPodsPatched(t *testing.T, ctlClient client.Reader, ctx context.Context, expectedPatchPods int) {
	objList := corev1.PodList{}
	require.NoError(t, ctlClient.List(ctx, &objList, client.MatchingLabels{watchdogPatchedLabel: "true"}))
	require.Len(t, objList.Items, expectedPatchPods)
}

// assertExpectedPodsDeleted
func assertExpectedPodsDeleted(t *testing.T, pods []*corev1.Pod, ctlClient client.Reader, ctx context.Context, daprized int, running int, injected int) {
	for i, pod := range pods {
		err := ctlClient.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{})
		injectedOrRunning := running
		if injected > injectedOrRunning {
			injectedOrRunning = injected
		}
		if i < daprized && i >= injectedOrRunning {
			require.Error(t, err)
			require.True(t, apierrors.IsNotFound(err))
		} else {
			require.NoError(t, err)
		}
	}
}

func Test_patchPodLabel(t *testing.T) {
	tests := []struct {
		name       string
		pod        *corev1.Pod
		wantLabels map[string]string
		wantErr    bool
	}{
		{
			name:       "nilLabels",
			pod:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test"}},
			wantLabels: map[string]string{watchdogPatchedLabel: "true"},
		},
		{
			name:       "emptyLabels",
			pod:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test", Labels: map[string]string{}}},
			wantLabels: map[string]string{watchdogPatchedLabel: "true"},
		},
		{
			name:       "nonEmptyLabels",
			pod:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test", Labels: map[string]string{"app": "name"}}},
			wantLabels: map[string]string{watchdogPatchedLabel: "true", "app": "name"},
		},
		{
			name:    "noName",
			pod:     &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "name"}}},
			wantErr: true,
		},
		{
			name:       "alreadyPresent",
			pod:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "name", watchdogPatchedLabel: "true"}}},
			wantLabels: map[string]string{watchdogPatchedLabel: "true", "app": "name"},
		},
	}
	for _, tc := range tests {
		ctlClient := fake.NewClientBuilder().WithObjects(tc.pod).Build()
		t.Run(tc.name, func(t *testing.T) {
			if err := patchPodLabel(context.TODO(), ctlClient, tc.pod); (err != nil) != tc.wantErr {
				t.Fatalf("patchPodLabel() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				require.Equal(t, tc.wantLabels, tc.pod.Labels)
			}
		})
	}
}

func TestDaprWatchdog_Start(t *testing.T) {
	// simple test of start
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := false
	defer func() {
		if !cancelled {
			cancel()
		}
	}()

	// change log for tests
	singleIterationDurationThreshold = 100 * time.Millisecond
	defer func() {
		singleIterationDurationThreshold = time.Second
		sidecarInjectorWaitInterval = 100 * time.Millisecond
	}()

	ctlClient := fake.NewClientBuilder().WithObjects(createMockInjectorDeployment(1)).Build()
	dw := &DaprWatchdog{
		client:            ctlClient,
		maxRestartsPerMin: 0,
		canPatchPodLabels: true,
		interval:          200 * time.Millisecond,
	}
	daprized := 5
	running := 3
	injected := 1
	pods := createMockPods(10, daprized, injected, running)
	for _, pod := range pods {
		require.NoError(t, ctlClient.Create(ctx, pod))
	}

	startDone := make(chan struct{}, 1)
	go func() {
		require.NoError(t, dw.Start(ctx))
		startDone <- struct{}{}
	}()

	// let it run a few cycles
	time.Sleep(time.Second)
	cancel()
	cancelled = true

	<-startDone

	t.Log("daprized pods should be deleted except those running")
	assertExpectedPodsDeleted(t, pods, ctlClient, ctx, daprized, running, injected)
	t.Log("daprized pods with sidecar should have been patched")
	assertExpectedPodsPatched(t, ctlClient, ctx, 2)
}
