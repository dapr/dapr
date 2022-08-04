package operator

import (
	"context"
	"time"

	"go.uber.org/ratelimit"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/utils"
)

const (
	sidecarContainerName     = "daprd"
	daprEnabledAnnotationKey = "dapr.io/enabled"
)

// DaprWatchdog is a controller that periodically polls all pods and ensures that they are in the correct state.
// This controller only runs on the cluster's leader.
// Currently, this ensures that the sidecar is injected in each pod, otherwise it kills the pod so it can be restarted.
type DaprWatchdog struct {
	enabled           bool
	interval          time.Duration
	maxRestartsPerMin int

	client         client.Client
	restartLimiter ratelimit.Limiter
}

// NeedLeaderElection makes it so the controller runs on the leader node only.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.LeaderElectionRunnable .
func (dw *DaprWatchdog) NeedLeaderElection() bool {
	return true
}

// Start the controller. This method blocks until the context is canceled.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable .
func (dw *DaprWatchdog) Start(parentCtx context.Context) error {
	if !dw.enabled {
		log.Infof("DaprWatchdog is not enabled")
		return nil
	}

	log.Infof("DaprWatchdog worker starting")

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	if dw.maxRestartsPerMin > 0 {
		dw.restartLimiter = ratelimit.New(
			dw.maxRestartsPerMin,
			ratelimit.Per(time.Minute),
			ratelimit.WithoutSlack,
		)
	} else {
		dw.restartLimiter = ratelimit.NewUnlimited()
	}

	// Use a buffered channel to make sure that there's at most one iteration at a given time
	// and if an iteration isn't done by the time the next one is scheduled to start, no more iterations are added to the queue
	workCh := make(chan struct{}, 1)
	defer close(workCh)
	firstCompleteCh := make(chan struct{})
	go func() {
		defer log.Infof("DaprWatchdog worker stopped")
		for {
			select {
			case <-ctx.Done():
				return
			case <-workCh:
				dw.listPods(ctx)
				if firstCompleteCh != nil {
					close(firstCompleteCh)
					firstCompleteCh = nil
				}
			}
		}
	}()

	log.Infof("DaprWatchdog worker started")

	// Start an iteration right away, at startup, then wait for completion
	workCh <- struct{}{}
	<-firstCompleteCh

	// If we only run once, exit when it's done
	if dw.interval < time.Second {
		return nil
	}

	// Repeat on interval
	t := time.NewTicker(dw.interval)
	defer t.Stop()

forloop:
	for {
		select {
		case <-ctx.Done():
			break forloop
		case <-t.C:
			log.Debugf("DaprWatchdog worker tick")
			select {
			case workCh <- struct{}{}:
				// Successfully queued another iteration
				log.Debugf("Added listPods iteration to the queue")
			default:
				// Do nothing - there's already an iteration in the queue
				log.Debugf("There's already an iteration in the listPods queue-not adding another one")
			}
		}
	}

	log.Infof("DaprWatchdog worker stopping")
	return nil
}

func (dw *DaprWatchdog) listPods(ctx context.Context) {
	log.Infof("DaprWatchdog started checking pods")

	// Request the list of pods
	// We are not using pagination because we may be deleting pods during the iterations
	// The client implements some level of caching anyways
	pod := &corev1.PodList{}
	err := dw.client.List(ctx, pod)
	if err != nil {
		log.Errorf("Failed to list pods. Error: %v", err)
		return
	}

	for _, v := range pod.Items {
		// Skip invalid pods
		if v.Name == "" || len(v.Spec.Containers) == 0 {
			continue
		}

		logName := v.Namespace + "/" + v.Name

		// Filter for pods with the dapr.io/enabled annotation
		if daprEnabled, ok := v.Annotations[daprEnabledAnnotationKey]; !ok || !utils.IsTruthy(daprEnabled) {
			log.Debugf("Skipping pod %s: %s is not true", logName, daprEnabledAnnotationKey)
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
			log.Debugf("Found Dapr sidecar in pod %s", logName)
			continue
		}

		// Pod doesn't have a sidecar, so we need to kill it so it can be restarted and have the sidecar injected
		log.Warnf("Pod %s does not have the Dapr sidecar and will be deleted", logName)
		err = dw.client.Delete(ctx, &v)
		if err != nil {
			log.Errorf("Failed to delete pod %s. Error: %v", logName, err)
			continue
		}

		log.Infof("Deleted pod %s", logName)

		log.Debugf("Taking a pod restart token")
		before := time.Now()
		_ = dw.restartLimiter.Take()
		log.Debugf("Resumed after pausing for %v", time.Now().Sub(before))
	}

	log.Infof("DaprWatchdog completed checking pods")
}
