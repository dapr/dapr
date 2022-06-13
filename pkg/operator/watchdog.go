package operator

import (
	"context"
	"time"
)

// DaprWatchdog is a controller that periodically polls all pods and ensures that they are in the correct state.
// Currently, this ensures that the sidecar is injected in each pod, otherwise it kills the pod so it can be restarted.
type DaprWatchdog struct{}

// NeedLeaderElection makes it so the controller runs on the leader node only.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.LeaderElectionRunnable .
func (dw *DaprWatchdog) NeedLeaderElection() bool {
	return true
}

// Start the controller. This method blocks until the context is canceled.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable .
func (dw *DaprWatchdog) Start(ctx context.Context) error {
	log.Infof("DaprWatchdog started")
	t := time.NewTicker(time.Second)
forloop:
	for {
		select {
		case <-ctx.Done():
			t.Stop()
			break forloop
		case <-t.C:
			log.Infof("DaprWatchdog tick")
		}
	}
	log.Infof("DaprWatchdog stopping")
	return nil
}
