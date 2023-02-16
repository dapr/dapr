package operator

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/selection"

	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.uber.org/ratelimit"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/utils"
)

const (
	sidecarContainerName          = "daprd"
	daprEnabledAnnotationKey      = "dapr.io/enabled"
	sidecarInjectorDeploymentName = "dapr-sidecar-injector"
	sidecarInjectorWaitInterval   = 5 * time.Second // How long to wait for the sidecar injector deployment to be up and running before retrying
)

// DaprWatchdog is a controller that periodically polls all pods and ensures that they are in the correct state.
// This controller only runs on the cluster's leader.
// Currently, this ensures that the sidecar is injected in each pod, otherwise it kills the pod so it can be restarted.
type DaprWatchdog struct {
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
	log.Infof("DaprWatchdog worker starting")

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	sel := labels.NewSelector()
	// TODO: 'dapr.io/sidecar-injected' should be replaced with sidecar.SidecarInjectedLabel when https://github.com/dapr/dapr/pull/5937/files is merged
	req, err := labels.NewRequirement("dapr.io/sidecar-injected", selection.DoesNotExist, []string{})
	if err != nil {
		log.Fatalf("Unable to add label requirement to find pods with Injector created label , err: %s", err)
	}
	sel.Add(*req)

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
			case _, ok := <-workCh:
				if !ok {
					continue
				}
				ok = dw.listPods(ctx, sel)
				if firstCompleteCh != nil {
					if ok {
						close(firstCompleteCh)
						firstCompleteCh = nil
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

func (dw *DaprWatchdog) listPods(ctx context.Context, podsNotMatchingInjectorLabelSelector labels.Selector) bool {
	log.Infof("DaprWatchdog started checking pods")

	// Look for the dapr-sidecar-injector deployment first and ensure it's running
	// Otherwise, the pods would come back up without the sidecar again
	deployment := &appsv1.DeploymentList{}

	err := dw.client.List(ctx, deployment, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(
			map[string]string{"app": sidecarInjectorDeploymentName},
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

	// Request the list of pods
	// We are not using pagination because we may be deleting pods during the iterations
	// The client implements some level of caching anyway
	podList := &metav1.PartialObjectMetadataList{}
	podList.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PodList"))
	err = dw.client.List(ctx, podList, client.MatchingLabelsSelector{Selector: podsNotMatchingInjectorLabelSelector})
	if err != nil {
		log.Errorf("Failed to list pods. Error: %v", err)
		return false
	}

	var potentialPods []types.NamespacedName

	// first let's check if there is anything we can quickly skip from the metadata not having dapr annotation
	for _, v := range podList.Items {
		// Skip invalid pods
		if v.Name == "" {
			continue
		}

		logName := v.Namespace + "/" + v.Name

		// Filter for pods with the dapr.io/enabled annotation
		if daprEnabled, ok := v.Annotations[daprEnabledAnnotationKey]; !ok || !utils.IsTruthy(daprEnabled) {
			log.Debugf("Skipping pod %s: %s is not true", logName, daprEnabledAnnotationKey)
			continue
		}
		potentialPods = append(potentialPods, types.NamespacedName{Name: v.Name, Namespace: v.Namespace})
	}

	// let's now get more detail pod information from those pods with the annotation we found on our previous check
	for i := 0; i < len(potentialPods); i++ {
		pod := corev1.Pod{}
		if err := dw.client.Get(ctx, potentialPods[i], &pod); err != nil {
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
		logName := pod.Namespace + "/" + pod.Name
		if hasSidecar {
			log.Debugf("Found Dapr sidecar in pod %s", logName)
			continue
		}

		// Pod doesn't have a sidecar, so we need to kill it so it can be restarted and have the sidecar injected
		log.Warnf("Pod %s does not have the Dapr sidecar and will be deleted", logName)
		//nolint:gosec
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
