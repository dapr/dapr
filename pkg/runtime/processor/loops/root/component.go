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
	"errors"
	"fmt"
	"strings"
	"time"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/backoff"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/runtime/compstore"
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

// registerInitRetry arms a supersession signal for an init retry loop on
// name, cancelling any previous loop for the same name.
func (r *Root) registerInitRetry(name string) chan struct{} {
	r.initRetryMu.Lock()
	defer r.initRetryMu.Unlock()
	if prev, ok := r.initRetryCancels[name]; ok {
		close(prev)
	}
	ch := make(chan struct{})
	r.initRetryCancels[name] = ch
	return ch
}

func (r *Root) unregisterInitRetry(name string, ch chan struct{}) {
	r.initRetryMu.Lock()
	defer r.initRetryMu.Unlock()
	if cur, ok := r.initRetryCancels[name]; ok && cur == ch {
		delete(r.initRetryCancels, name)
	}
}

func (r *Root) cancelInitRetry(name string) {
	r.initRetryMu.Lock()
	defer r.initRetryMu.Unlock()
	if ch, ok := r.initRetryCancels[name]; ok {
		close(ch)
		delete(r.initRetryCancels, name)
	}
}

func (r *Root) handleInit(ctx context.Context, ev *loops.Init) {
	comp := ev.Component

	// A new configuration supersedes any in-flight retry loop for this name.
	// The signal is armed before the first attempt so a Close or re-create
	// landing mid-attempt is never lost. Retry attempts bypass this handler.
	var superseded chan struct{}
	if shouldRetryInit(comp) {
		superseded = r.registerInitRetry(comp.Name)
	} else {
		r.cancelInitRetry(comp.Name)
	}

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
		if superseded != nil {
			defer r.unregisterInitRetry(comp.Name, superseded)
		}
		if innerErr != nil && superseded != nil {
			select {
			case <-superseded:
				innerErr = fmt.Errorf("superseded by a newer component configuration during init retry: %w", innerErr)
				abandoned = true
			default:
				// An EarlyResult caller (the hot reload reconciler) must not
				// stay blocked behind the retry loop: hand it the first
				// attempt's error before retrying.
				if ev.EarlyResult {
					sendResult(ev.Result, rterrors.NewInit(rterrors.InitComponentFailure, comp.LogName(), innerErr))
					ev.Result = nil
				}
				innerErr, abandoned = r.retryInit(catLoop, comp, timeout, innerErr, superseded)
			}
		}
		// A success which raced a newer spec or a delete has committed a stale
		// component: roll it back so the newer event converges.
		if innerErr == nil && superseded != nil && r.rollbackStaleInit(catLoop, comp, superseded) {
			innerErr = errSupersededInit
			abandoned = true
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
// it succeeds, the runtime shuts down, or the configuration is superseded.
// Returns the final error and whether the loop was abandoned.
func (r *Root) retryInit(catLoop loop.Interface[loops.EventCategory], comp compapi.Component, timeout time.Duration, firstErr error, superseded <-chan struct{}) (error, bool) {
	retryBackoff := backoff.NewJitter(initRetryBackoffBase, initRetryBackoffCap)
	err := firstErr
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		delay := retryBackoff.NextBackOff()
		log.Warnf("Retrying init of actor state store %s in %s after error: %s", comp.LogName(), delay, err)
		timer.Reset(delay)
		select {
		case <-r.runCtx.Done():
			return err, true
		case <-superseded:
			log.Infof("Stopping init retry of %s: configuration superseded", comp.LogName())
			return fmt.Errorf("superseded by a newer component configuration during init retry: %w", err), true
		case <-timer.C:
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
		// A stale commit from a superseded attempt can occupy the slot; close
		// it so the next attempt of this (current) configuration can install.
		// Guarded on the supersession signal: a superseded loop must never
		// close the newer component, so it abandons at the top of the loop.
		if errors.Is(err, compstore.ErrComponentAlreadyExists) {
			select {
			case <-superseded:
			default:
				log.Warnf("Closing stale component occupying the slot of %s before retrying init", comp.LogName())
				catLoop.Enqueue(&loops.Close{Component: comp})
			}
		}
	}
}

var errSupersededInit = errors.New("superseded by a newer component configuration during init")

// rollbackStaleInit closes the just-committed component if its configuration
// was superseded or deleted while the successful init attempt was in flight.
func (r *Root) rollbackStaleInit(catLoop loop.Interface[loops.EventCategory], comp compapi.Component, superseded <-chan struct{}) bool {
	select {
	case <-superseded:
	default:
		return false
	}
	log.Warnf("Init of %s succeeded after its configuration was superseded or removed; closing the stale component", comp.LogName())
	catLoop.Enqueue(&loops.Close{Component: comp})
	return true
}

func (r *Root) handleClose(_ context.Context, ev *loops.Close) {
	comp := ev.Component

	r.cancelInitRetry(comp.Name)
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
