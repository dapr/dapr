package operator

import (
	"context"
	"time"

	"github.com/dapr/dapr/utils"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	sidecarContainerName     = "daprd"
	daprEnabledAnnotationKey = "dapr.io/enabled"
)

// DaprWatchdog is a controller that periodically polls all pods and ensures that they are in the correct state.
// This controller only runs on the cluster's leader.
// Currently, this ensures that the sidecar is injected in each pod, otherwise it kills the pod so it can be restarted.
type DaprWatchdog struct {
	client client.Client
}

// NeedLeaderElection makes it so the controller runs on the leader node only.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.LeaderElectionRunnable .
func (dw *DaprWatchdog) NeedLeaderElection() bool {
	return true
}

// Start the controller. This method blocks until the context is canceled.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable .
func (dw *DaprWatchdog) Start(ctx context.Context) error {
	log.Infof("DaprWatchdog started")
	t := time.NewTicker(30 * time.Second)
forloop:
	for {
		select {
		case <-ctx.Done():
			t.Stop()
			break forloop
		case <-t.C:
			log.Infof("DaprWatchdog tick")
			dw.listPods(ctx)
		}
	}
	log.Infof("DaprWatchdog stopping")
	return nil
}

func (dw *DaprWatchdog) listPods(ctx context.Context) {
	pod := &corev1.PodList{}
	err := dw.client.List(ctx, pod,
		client.InNamespace(GetNamespace()),
	)
	if err != nil {
		log.Errorf("Failed to list pods. Error: %v", err)
		return
	}

	for _, v := range pod.Items {
		// Skip invalid pods
		if v.Name == "" || len(v.Spec.Containers) == 0 {
			continue
		}

		// Filter for pods with the dapr.io/enabled annotation
		if daprEnabled, ok := v.Annotations[daprEnabledAnnotationKey]; !ok || !utils.IsTruthy(daprEnabled) {
			log.Debugf("Skipping pod %s: %s is not enabled", v.Name, daprEnabledAnnotationKey)
			continue
		}

		// Check if the sidecar container is running
		hasSidecar := false
		for _, c := range v.Spec.Containers {
			if c.Name == sidecarContainerName {
				hasSidecar = true
				break
			}
		}
		if hasSidecar {
			log.Debugf("Found Dapr sidecar in pod %s", v.Name)
			continue
		}

		// Pod doesn't have a sidecar, so we need to kill it so it can be restarted and have the sidecar injected
		log.Warnf("Pod %s does not have the Dapr sidecar and will be deleted", v.Name)
		err = dw.client.Delete(ctx, &v)
		if err != nil {
			log.Errorf("Failed to delete pod %s. Error: %v", v.Name, err)
			continue
		}

		log.Infof("Deleted pod %s", v.Name)
	}
}
