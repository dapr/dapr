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
	"time"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

const defaultComponentInitTimeout = time.Second * 5

func (r *Root) handleInit(ctx context.Context, ev *loops.Init) {
	comp := ev.Component

	// Preprocess: resolve secret refs on the component, detect unresolved
	// secret-store dependencies.
	_, unreadyStore := r.secret.ProcessResource(ctx, &comp)
	if unreadyStore != "" {
		r.pendingDependents[unreadyStore] = append(r.pendingDependents[unreadyStore], comp)
		// Defer indication: report success to caller (matches legacy semantics
		// where AddPendingComponent returns true even when a component is
		// queued behind an unready secret store).
		sendResult(ev.Result, nil)
		return
	}

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

	timeout, err := time.ParseDuration(comp.Spec.InitTimeout)
	if err != nil || timeout <= 0 {
		timeout = defaultComponentInitTimeout
	}
	initCtx, cancel := context.WithTimeout(ctx, timeout)

	// Intercept the Result so we can flush dependents on a successful secret
	// store init. The timeout is propagated so the instance loop bounds the
	// actual component init with the same deadline the finalizer waits on,
	// rather than letting a timed-out init keep running and commit late.
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
		defer cancel()
		var innerErr error
		select {
		case <-initCtx.Done():
			innerErr = fmt.Errorf("init timeout for component %s", comp.LogName())
		case e := <-intercept:
			innerErr = e
		}
		if errors.Is(initCtx.Err(), context.DeadlineExceeded) && innerErr == nil {
			innerErr = fmt.Errorf("init timeout for component %s", comp.LogName())
		}
		// Forward the result to the caller immediately so callers do not
		// block on a pending event if the root loop is shutting down.
		if innerErr != nil {
			log.Errorf("Failed to init component %s: %s", comp.LogName(), innerErr)
			sendResult(ev.Result, rterrors.NewInit(rterrors.InitComponentFailure, comp.LogName(), innerErr))
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
