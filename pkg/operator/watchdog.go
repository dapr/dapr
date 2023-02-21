package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"

	"go.uber.org/ratelimit"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/injector/sidecar"
	"github.com/dapr/dapr/utils"
)

const (
	sidecarContainerName          = "daprd"
	daprEnabledAnnotationKey      = "dapr.io/enabled"
	sidecarInjectorDeploymentName = "dapr-sidecar-injector"
	sidecarInjectorWaitInterval   = 5 * time.Second // How long to wait for the sidecar injector deployment to be up and running before retrying
	watchdogPatchedLabel          = "dapr.io/watchdog-patched"
)

// DaprWatchdog is a controller that periodically polls all pods and ensures that they are in the correct state.
// This controller only runs on the cluster's leader.
// Currently, this ensures that the sidecar is injected in each pod, otherwise it kills the pod, so it can be restarted.
type DaprWatchdog struct {
	interval          time.Duration
	maxRestartsPerMin int

	client            client.Client
	nonCachedClient   client.Reader
	restartLimiter    ratelimit.Limiter
	canPatchPodLabels bool
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

	// negative selector to be used during list pod operations
	sel := getSideCarInjectedNotExistsSelector()

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

// getSideCarInjectedNotExistsSelector creates a selector that matches pod without the injector patched label
func getSideCarInjectedNotExistsSelector() labels.Selector {
	sel := labels.NewSelector()
	req, err := labels.NewRequirement(sidecar.SidecarInjectedLabel, selection.DoesNotExist, []string{})
	if err != nil {
		log.Fatalf("Unable to add label requirement to find pods with Injector created label , err: %s", err)
	}
	sel.Add(*req)
	req, err = labels.NewRequirement(watchdogPatchedLabel, selection.DoesNotExist, []string{})
	if err != nil {
		log.Fatalf("Unable to add label requirement to find pods with Watchdog created label , err: %s", err)
	}
	sel.Add(*req)
	return sel
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

	// We are splitting the process of finding the potential pods by first querying only for the metadata of the pods
	// to verify the annotation.  If we find some with dapr enabled annotation we will subsequently query those further.

	// Request the list of pods metadata
	// We are not using pagination because we may be deleting pods during the iterations
	// The client implements some level of caching anyway
	podList := &metav1.PartialObjectMetadataList{}
	podList.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("PodList"))
	err = dw.client.List(ctx, podList, client.MatchingLabelsSelector{Selector: podsNotMatchingInjectorLabelSelector})
	if err != nil {
		log.Errorf("Failed to list pods. Error: %v", err)
		return false
	}

	podsMaybeMissingSidecar := make([]types.NamespacedName, 0, len(podList.Items))

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
		podsMaybeMissingSidecar = append(podsMaybeMissingSidecar, types.NamespacedName{Name: v.Name, Namespace: v.Namespace})
	}

	// let's now get more detail pod information from those pods with the annotation we found on our previous check
	for _, podNamespaceName := range podsMaybeMissingSidecar {
		pod := corev1.Pod{}
		// get pod that might be missing sidecar. we are using the non-cached client if we know we can patch as
		// we'll know that the next time we will probably not get back here as the list watch should have removed it
		cl := dw.client.(client.Reader)
		if dw.canPatchPodLabels {
			cl = dw.nonCachedClient
		}
		if err = cl.Get(ctx, podNamespaceName, &pod); err != nil {
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

		// Pod doesn't have a sidecar, so we need to kill it, so it can be restarted and have the sidecar injected
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
	oldPodData, err := json.Marshal(pod)
	if err != nil {
		return fmt.Errorf("problems marshaling pod to patch label in watchdog, err: %w", err)
	}
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[watchdogPatchedLabel] = "true"
	newPodData, err := json.Marshal(pod)
	if err != nil {
		return fmt.Errorf("problems marshaling pod to patch label in watchdog, err: %w", err)
	}
	mergePatch, err := jsonpatch.CreateMergePatch(oldPodData, newPodData)
	if err != nil {
		return fmt.Errorf("problems creating json merge patch to patch pod label, err: %w", err)
	}
	return cl.Patch(ctx, pod, client.RawPatch(types.MergePatchType, mergePatch))
}
