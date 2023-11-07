package operator

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/ratelimit"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	operatorConsts "github.com/dapr/dapr/pkg/operator/meta"
	"github.com/dapr/kit/utils"
)

const (
	sidecarContainerName     = "daprd"
	daprEnabledAnnotationKey = "dapr.io/enabled"
)

// service timers, using var to be able to mock their values in tests
var (
	// minimum amount of time that interval should be to not execute this only once
	singleIterationDurationThreshold = time.Second
	// How long to wait for the sidecar injector deployment to be up and running before retrying
	sidecarInjectorWaitInterval = 5 * time.Second
)

// DaprWatchdog is a controller that periodically polls all pods and ensures that they are in the correct state.
// This controller only runs on the cluster's leader.
// Currently, this ensures that the sidecar is injected in each pod, otherwise it kills the pod, so it can be restarted.
type DaprWatchdog struct {
	interval          time.Duration
	maxRestartsPerMin int

	client            client.Client
	restartLimiter    ratelimit.Limiter
	canPatchPodLabels bool
	podSelector       labels.Selector
}

// NeedLeaderElection makes it so the controller runs on the leader node only.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.LeaderElectionRunnable .
func (dw *DaprWatchdog) NeedLeaderElection() bool {
	return true
}

// Start the controller. This method blocks until the context is canceled.
// Implements sigs.k8s.io/controller-runtime/pkg/manager.Runnable .
func (dw *DaprWatchdog) Start(parentCtx context.Context) error {
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
		firstCompleted := false
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-workCh:
				if !ok {
					continue
				}
				ok = dw.listPods(ctx)
				if !firstCompleted {
					if ok {
						close(firstCompleteCh)
						firstCompleted = true
					} else {
						// Ensure that there's at least one successful run
						// If it failed, retry after a bit
						time.Sleep(sidecarInjectorWaitInterval)
						workCh <- struct{}{}
					}
				}
			}
		}
	}()

	log.Infof("DaprWatchdog worker started")

	// Start an iteration right away, at startup
	workCh <- struct{}{}
	// Wait for completion of first iteration
	select {
	case <-ctx.Done(): // in case context Done, as first iteration can get stuck, and the channel would not be closed
		return nil
	case <-firstCompleteCh:
		// nop
	}

	// If we only run once, exit when it's done
	if dw.interval < singleIterationDurationThreshold {
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

// getSideCarInjectedNotExistsSelector creates a selector that matches pod without the injector patched label
func getSideCarInjectedNotExistsSelector() labels.Selector {
	sel := labels.NewSelector()
	req, err := labels.NewRequirement(injectorConsts.SidecarInjectedLabel, selection.DoesNotExist, []string{})
	if err != nil {
		log.Fatalf("Unable to add label requirement to find pods with Injector created label , err: %s", err)
	}
	sel = sel.Add(*req)
	req, err = labels.NewRequirement(operatorConsts.WatchdogPatchedLabel, selection.DoesNotExist, []string{})
	if err != nil {
		log.Fatalf("Unable to add label requirement to find pods with Watchdog created label , err: %s", err)
	}
	return sel.Add(*req)
}

func (dw *DaprWatchdog) listPods(ctx context.Context) bool {
	log.Infof("DaprWatchdog started checking pods")

	// Look for the dapr-sidecar-injector deployment first and ensure it's running
	// Otherwise, the pods would come back up without the sidecar again
	deployment := &appsv1.DeploymentList{}

	err := dw.client.List(ctx, deployment, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(
			map[string]string{"app": operatorConsts.SidecarInjectorDeploymentName},
		),
	})
	if err != nil {
		log.Errorf("Failed to list pods to get dapr-sidecar-injector. Error: %v", err)
		return false
	}
	if len(deployment.Items) == 0 || deployment.Items[0].Status.ReadyReplicas < 1 {
		log.Warnf("Could not find a running dapr-sidecar-injector; will retry later")
		return false
	}

	log.Debugf("Found running dapr-sidecar-injector container")

	// We are splitting the process of finding the potential pods by first querying only for the metadata of the pods
	// to verify the annotation.  If we find some with dapr enabled annotation we will subsequently query those further.

	podListOpts := &client.ListOptions{
		LabelSelector: dw.podSelector,
	}

	podList := &corev1.PodList{}
	err = dw.client.List(ctx, podList, podListOpts)
	if err != nil {
		log.Errorf("Failed to list pods. Error: %v", err)
		return false
	}

	for i := range podList.Items {
		pod := podList.Items[i]
		// Skip invalid pods
		if pod.Name == "" {
			continue
		}

		logName := pod.Namespace + "/" + pod.Name

		// Filter for pods with the dapr.io/enabled annotation
		if daprEnabled, ok := pod.Annotations[daprEnabledAnnotationKey]; !(ok && utils.IsTruthy(daprEnabled)) {
			log.Debugf("Skipping pod %s: %s is not true", logName, daprEnabledAnnotationKey)
			continue
		}

		// Check if the sidecar container is running
		hasSidecar := false
		for _, c := range pod.Spec.Containers {
			if c.Name == sidecarContainerName {
				hasSidecar = true
				break
			}
		}
		if hasSidecar {
			if dw.canPatchPodLabels {
				log.Debugf("Found Dapr sidecar in pod %s, will patch the pod labels", logName)
				err = patchPodLabel(ctx, dw.client, &pod)
				if err != nil {
					log.Errorf("problems patching pod %s, err: %s", logName, err)
				}
			} else {
				log.Debugf("Found Dapr sidecar in pod %s", logName)
			}
			continue
		}

		// Pod doesn't have a sidecar, so we need to delete it, so it can be restarted and have the sidecar injected
		log.Warnf("Pod %s does not have the Dapr sidecar and will be deleted", logName)
		err = dw.client.Delete(ctx, &pod)
		if err != nil {
			log.Errorf("Failed to delete pod %s. Error: %v", logName, err)
			continue
		}

		log.Infof("Deleted pod %s", logName)

		log.Debugf("Taking a pod restart token")
		before := time.Now()
		_ = dw.restartLimiter.Take()
		log.Debugf("Resumed after pausing for %v", time.Since(before))
	}

	log.Infof("DaprWatchdog completed checking pods")

	return true
}

func patchPodLabel(ctx context.Context, cl client.Client, pod *corev1.Pod) error {
	// in case this has been already patched just return
	if _, ok := pod.GetLabels()[operatorConsts.WatchdogPatchedLabel]; ok {
		return nil
	}
	mergePatch := []byte(fmt.Sprintf(`{"metadata":{"labels":{"%s":"true"}}}`, operatorConsts.WatchdogPatchedLabel))
	return cl.Patch(ctx, pod, client.RawPatch(types.MergePatchType, mergePatch))
}
