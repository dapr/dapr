/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package root

import (
	"context"
	"fmt"
	"strings"
	"time"

	wfcommon "github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	procstate "github.com/dapr/dapr/pkg/runtime/processor/state"
	"github.com/dapr/kit/events/loop"
	kitstrings "github.com/dapr/kit/strings"
)

// DefaultComponentInitTimeout is the init deadline applied to a component
// whose spec does not set a valid InitTimeout. It is exported so the
// processor's inline (test) init path applies the same default.
const DefaultComponentInitTimeout = time.Second * 5

func (r *Root) handleInit(ctx context.Context, ev *loops.Init) {
	comp := ev.Component

	// Preprocess: resolve secret refs on the component, detect unresolved
	// secret-store dependencies.
	_, unreadyStore := r.secret.ProcessResource(ctx, &comp)
	if unreadyStore != "" {
		// Dedupe by name: the hot reload reconciler re-creates a parked
		// component on every reconcile (it is not in the component store
		// while parked), which would otherwise grow the parked list and
		// double-init on flush.
		deps := r.pendingDependents[unreadyStore]
		replaced := false
		for i := range deps {
			if deps[i].Name == comp.Name {
				deps[i] = comp
				replaced = true
				break
			}
		}
		if !replaced {
			r.pendingDependents[unreadyStore] = append(deps, comp)
		}
		// Defer indication: report success to caller (matches legacy semantics
		// where AddPendingComponent returns true even when a component is
		// queued behind an unready secret store).
		sendResult(ev.Result, nil)
		// An Internal re-enqueue was pre-counted in inFlight. The component is
		// parked again behind another store, so release its slot now; it will
		// be pre-counted again when that store completes. Without this the
		// counter never returns to zero and barriers hang.
		if ev.Internal {
			r.decInFlight()
		}
		return
	}

	cat := r.category(comp)
	if cat == "" {
		sendResult(ev.Result, fmt.Errorf("incorrect type %s", comp.Spec.Type))
		if ev.Internal {
			r.decInFlight()
		}
		return
	}

	catLoop, ok := r.categories[cat]
	if !ok {
		sendResult(ev.Result, fmt.Errorf("unknown component category: %q", cat))
		if ev.Internal {
			r.decInFlight()
		}
		return
	}

	timeout, err := time.ParseDuration(comp.Spec.InitTimeout)
	if err != nil || timeout <= 0 {
		timeout = DefaultComponentInitTimeout
	}

	// Intercept the Result so we can flush dependents on a successful secret
	// store init. The timeout is propagated so the instance loop bounds the
	// actual component init with that deadline and synthesises a timeout error
	// (including when a timed-out init returns nil) before replying here.
	intercept := make(chan error, 1)
	catLoop.Enqueue(&loops.Init{
		Component: comp,
		Result:    intercept,
		Timeout:   timeout,
	})

	if !ev.Internal {
		r.inFlight++
	}
	r.finalizers.Go(func() {
		// Wait for the actual init result. Like the legacy synchronous Init,
		// callers stay blocked until the component's Init returns, even when it
		// overruns its deadline.
		innerErr := <-intercept
		// The actor state store retries in place of failing fatally: a
		// transient outage of its backing database (for example Postgres
		// restarting) must leave the runtime alive and unready rather than
		// crash it, which under Kubernetes turns a seconds-long database blip
		// into a crash-restart cycle of every replica that lasts until the
		// database returns. The caller stays blocked while retrying, so
		// readiness keeps reporting the degraded state.
		var abandoned bool
		if innerErr != nil && shouldRetryInit(comp) {
			innerErr, abandoned = r.retryInit(catLoop, comp, timeout, innerErr)
		}
		if innerErr != nil {
			log.Errorf("Failed to init component %s: %s", comp.LogName(), innerErr)
			wrapped := rterrors.NewInit(rterrors.InitComponentFailure, comp.LogName(), innerErr)
			sendResult(ev.Result, wrapped)
			// A non-ignored init failure is fatal to the runtime. Record it so
			// Run surfaces it on a path the runtime's init-context cancellation
			// cannot mask (the legacy processComponents runner did this), so a
			// failure that races with shutdown still propagates out of Run. A
			// retry loop abandoned by shutdown is not a failure and is not
			// recorded.
			if !comp.Spec.IgnoreErrors && !abandoned {
				r.recordFatalInitError(fmt.Errorf("process component %s error: %w", comp.Name, wrapped))
			}
		} else {
			log.Infof("Component loaded: %s", comp.LogName())
			sendResult(ev.Result, nil)
		}
		// Always notify the root loop so it can update the in-flight counter
		// (drives Barrier completion). For secret-store inits, the notification
		// also flushes dependents. UserChan is nil because the caller has
		// already been notified via the immediate send above.
		r.loop.Enqueue(&loops.InstanceInitDone{
			Category: string(cat),
			Name:     comp.Name,
			Err:      innerErr,
		})
	})
}

const (
	// initRetryBackoffBase and initRetryBackoffCap bound the jittered backoff
	// between init attempts of a retriable component.
	initRetryBackoffBase = 500 * time.Millisecond
	initRetryBackoffCap  = 10 * time.Second
)

// shouldRetryInit reports whether a failed init of comp is retried in place
// of being recorded as fatal. Only the component marked as the actor state
// store qualifies: its availability is a runtime dependency (workflows,
// actors) whose transient loss must degrade the runtime, not kill it.
func shouldRetryInit(comp compapi.Component) bool {
	if comp.Spec.IgnoreErrors || !strings.HasPrefix(comp.Spec.Type, "state.") {
		return false
	}
	for _, m := range comp.Spec.Metadata {
		if strings.EqualFold(m.Name, procstate.PropertyKeyActorStateStore) {
			return kitstrings.IsTruthy(m.Value.String())
		}
	}
	return false
}

// retryInit re-attempts a failed component init with jittered backoff until
// it succeeds or the runtime shuts down. Each attempt goes through the
// category loop like the original one, with the same per-attempt timeout.
// Returns the final error and whether the loop was abandoned by shutdown.
func (r *Root) retryInit(catLoop loop.Interface[loops.EventCategory], comp compapi.Component, timeout time.Duration, firstErr error) (error, bool) {
	backoff := wfcommon.NewJitterBackoff(initRetryBackoffBase, initRetryBackoffCap)
	err := firstErr
	for {
		delay := backoff.NextBackOff()
		log.Warnf("Retrying init of actor state store %s in %s after error: %s", comp.LogName(), delay, err)
		select {
		case <-r.runCtx.Done():
			return err, true
		case <-time.After(delay):
		}

		res := make(chan error, 1)
		catLoop.Enqueue(&loops.Init{
			Component: comp,
			Result:    res,
			Timeout:   timeout,
		})
		select {
		case err = <-res:
		case <-r.runCtx.Done():
			// The category loop may already be closed and the enqueued event
			// dropped, so do not wait on res unconditionally.
			return err, true
		}
		if err == nil {
			return nil, false
		}
	}
}

func (r *Root) handleClose(_ context.Context, ev *loops.Close) {
	comp := ev.Component
	cat := r.category(comp)
	if cat == "" {
		sendResult(ev.Result, fmt.Errorf("incorrect type %s", comp.Spec.Type))
		return
	}
	catLoop, ok := r.categories[cat]
	if !ok {
		sendResult(ev.Result, fmt.Errorf("unknown component category: %q", cat))
		return
	}
	catLoop.Enqueue(ev)
}

func (r *Root) handleInstanceInitDone(ev *loops.InstanceInitDone) {
	// Forward the result to the caller, if there is one and it has not
	// already been served by the finalizer goroutine.
	if ev.UserChan != nil {
		sendResult(ev.UserChan, ev.Err)
	}

	// If this was a secret store coming online, gather and re-enqueue any
	// dependents. We pre-increment inFlight for each dependent so the
	// Barrier does not see a transient zero between completion of the parent
	// and dispatch of the dependents. The dependents are enqueued with
	// Internal=true so handleInit does not double count.
	var deps []compapi.Component
	if ev.Err == nil && components.Category(ev.Category) == components.CategorySecretStore {
		deps = r.pendingDependents[ev.Name]
		delete(r.pendingDependents, ev.Name)
	}
	r.inFlight += len(deps)
	for _, dep := range deps {
		r.loop.Enqueue(&loops.Init{Component: dep, Internal: true})
	}

	// Decrement for this completion. Release barriers if we reach zero.
	r.decInFlight()
}

// decInFlight decrements the in-flight counter and releases pending barriers
// once it reaches zero.
func (r *Root) decInFlight() {
	if r.inFlight > 0 {
		r.inFlight--
	}
	if r.inFlight == 0 {
		for _, done := range r.pendingBarriers {
			close(done)
		}
		r.pendingBarriers = nil
	}
}
