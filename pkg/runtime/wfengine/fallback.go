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

package wfengine

import (
	"context"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
)

// fallbackDampingDefault is how long a fallback host keeps the workflow actor
// types after another daprd of the app gained an executor. Ownership of the
// app's instances then moves to the daprds with an executor; until it does,
// a turn of an instance the fallback host owns fails at once and its
// reminder is retried every one to two seconds. A placement round completes
// in well under a second, so the period exists only to ride out a worker
// which connects and leaves again, such as one crash looping: without it,
// every daprd of the app would register and unregister the types on each
// connect and leave, and each time rebalance the app's instances twice. Ten
// seconds covers the reconnect backoff of the SDKs and the first restart
// delay of a crash looping pod, while it keeps short the window in which
// the instances a fallback host owns wait.
const fallbackDampingDefault = 10 * time.Second

// fallbackDamping is the damping period. Overridden by a test-only env var,
// not a supported production knob.
func fallbackDamping() time.Duration {
	return common.EnvDurationOr("DAPR_WORKFLOW_TEST_FALLBACK_DAMPING", fallbackDampingDefault)
}

// fallbackSettleDefault is how long a daprd which does not host the workflow
// actor types waits, with no other daprd of the app having an executor,
// before it hosts them as a fallback host. The placement tables list only
// the daprds connected to placement: at cold start, or once placement is
// back after a restart, a daprd which connects before the daprd with the
// worker sees no executor until the other connects too, which their
// reconnect backoff of half a second, with jitter, puts some hundreds of
// milliseconds apart. A fallback host made on that first table would own a
// share of the app's instances for the whole damping period, and the calls
// to them would wait. Three seconds covers several reconnect attempts,
// while a daprd whose last executor leaves does not wait it: it keeps
// hosting in place (see reconcileFallback), so the app is never left
// without a host on that account.
const fallbackSettleDefault = 3 * time.Second

// fallbackSettle is the settle period. Overridden by a test-only env var,
// not a supported production knob.
func fallbackSettle() time.Duration {
	return common.EnvDurationOr("DAPR_WORKFLOW_TEST_FALLBACK_SETTLE", fallbackSettleDefault)
}

// fallbackRetryInterval is how long runFallback waits to apply the rule
// again after a registration failed.
const fallbackRetryInterval = time.Second

// fallback is the engine's state of the WorkflowsStagedOperations preview (see
// backends/actors/fallback.go). The rule, which each daprd applies alone:
// host the workflow actor types if this daprd has a local executor, or if no
// other daprd of the app has one. The decision depends only on those two
// facts, never on who hosts the workflow actor type itself, so hosting it
// cannot feed back into the decision.
//
// The connection callbacks register a local executor at once, as without the
// feature. runFallback owns the rest: whether another daprd has an executor,
// and the hosting of a daprd without a local executor. A change of hosting it
// decides is applied only once the desired hosting has held for a period: a
// daprd which hosts nothing becomes a fallback host once no other daprd has
// had an executor for the settle period, and a fallback host gives the types
// up once another daprd has had one for the damping period. A daprd whose last
// local executor leaves takes no period: it hosts the types already, so it
// keeps them as a fallback host in place, or gives them up at once if another
// daprd has an executor.
//
// Every daprd of the app must run a version which knows the feature before
// it is enabled: an older daprd with a worker does not register the executor
// actor type, so a daprd with the feature and no worker sees no executor,
// hosts the types as a fallback host for good, and owns a share of the
// app's instances which never run while it does.
type fallback struct {
	// runCtx is runFallback's context, guarded by the engine's
	// actorRegLock; nil until runFallback starts. No fallback registration
	// is made before, nor once runCtx is done, so shutdown does not
	// re-register; the connection callbacks then unregister on their own.
	runCtx context.Context

	// factoryCtx bounds the actor factories' background work. The
	// factories stay registered when this daprd turns into a fallback host
	// or back, so they outlive the work-item stream which registered them;
	// cancelled once Run has stopped the workflow worker.
	factoryCtx    context.Context
	factoryCancel context.CancelFunc

	// executorRegistered is set while the workflow actor types are
	// registered (the engine's actorsRegistered) with a local executor: the
	// executor actor type is registered too, and the types are advertised
	// to the scheduler. actorsRegistered without it is a fallback host.
	// Guarded by the engine's actorRegLock.
	executorRegistered bool

	// kick has runFallback apply the rule after a local executor connected
	// or left.
	kick chan struct{}

	// watch subscribes to whether another daprd of the app has an executor.
	// A field so unit tests can drive it.
	watch func(ctx context.Context) (<-chan bool, error)

	clock   clock.Clock
	damping time.Duration
	settle  time.Duration
}

func newFallback(watch func(ctx context.Context) (<-chan bool, error)) *fallback {
	factoryCtx, factoryCancel := context.WithCancel(context.Background())
	return &fallback{
		factoryCtx:    factoryCtx,
		factoryCancel: factoryCancel,
		kick:          make(chan struct{}, 1),
		watch:         watch,
		clock:         clock.RealClock{},
		damping:       fallbackDamping(),
		settle:        fallbackSettle(),
	}
}

// localExecutor reports whether a workflow worker or an in-process executor
// is connected to this daprd.
func (wfe *engine) localExecutor() bool {
	return wfe.getWorkItemsCount.Load() > 0 || wfe.mcpRegistrationCount.Load() > 0
}

// fallbackAllowed reports whether a fallback registration may be made:
// runFallback runs. Must be called with actorRegLock held.
func (wfe *engine) fallbackAllowed() bool {
	return wfe.fallback.runCtx != nil && wfe.fallback.runCtx.Err() == nil
}

// kickFallback has runFallback apply the rule again.
func (wfe *engine) kickFallback() {
	select {
	case wfe.fallback.kick <- struct{}{}:
	default:
	}
}

// registerLocalExecutor hosts the workflow actor types with a local
// executor, as the connection callbacks do without the feature. The caller
// has counted the executor, and has not called syncExecutorAvailable: the
// executor is marked available here, once the types are registered with it.
// Until then a turn fails at once, as on a fallback host, so the halts a
// registration waits on (of a factory still draining, or one whose drain
// just completed) wait on no workflow code: a turn queued for the connecting
// worker could not be delivered before its connection callback returns.
// ctx bounds the wait for the actor runtime. Must be called with
// actorRegLock held.
func (wfe *engine) registerLocalExecutor(ctx context.Context) error {
	fb := wfe.fallback

	// runFallback applies the rule again either way: a period it waits out
	// ends with an executor here, and goes on if the registration failed and
	// the caller rolls its count back.
	defer wfe.kickFallback()

	switch {
	case !wfe.actorsRegistered:
		log.Debug("Registering workflow actors")
		if err := wfe.backend.RegisterHostedActors(ctx, fb.factoryCtx, true); err != nil {
			return err
		}
		wfe.actorsRegistered = true
		fb.executorRegistered = true
	case !fb.executorRegistered:
		// In place: the instances this daprd owns stay here, and the
		// types stay hosted throughout.
		log.Infof("A workflow executor connected; this daprd is no longer a fallback host of app '%s'", wfe.appID)
		if err := wfe.backend.SetLocalExecutor(ctx, fb.factoryCtx, true); err != nil {
			return err
		}
		fb.executorRegistered = true
	}
	wfe.syncExecutorAvailable()
	return nil
}

// releaseLocalExecutor follows the last local executor leaving. The caller
// has called syncExecutorAvailable, which cancelled the parked completions
// before anything waits on actor turn locks, and a turn fails at once from
// then on. While runFallback runs, it decides whether the types stay as a
// fallback host or go. Otherwise, as before Run or at shutdown, they are
// unregistered at once, as without the feature. Must be called with
// actorRegLock held.
func (wfe *engine) releaseLocalExecutor(ctx context.Context) error {
	if !wfe.actorsRegistered {
		return nil
	}
	if wfe.fallbackAllowed() {
		wfe.kickFallback()
		return nil
	}
	log.Debug("Unregistering workflow actors")
	return wfe.releaseActors(ctx)
}

// reconcileFallback applies the rule for a daprd without a local executor.
// want is whether the placement tables have shown no other daprd of the app
// with an executor, the condition for a fallback host; held is whether the
// hosting want asks for has held for its period. It reports whether the
// hosting must change once that period passes, which runFallback then waits
// out. ctx bounds the wait for the actor runtime. Must be called with
// actorRegLock held.
func (wfe *engine) reconcileFallback(ctx context.Context, want, held bool) (bool, error) {
	fb := wfe.fallback

	if wfe.sweeping > 0 {
		// A disconnect or an MCP unregister is cancelling the parked
		// completions with the lock released, and releases the types once
		// they are cancelled, kicking runFallback then. A halt before that
		// would wait on turns whose cancellation it took away.
		return false, nil
	}

	if wfe.localExecutor() {
		// The connection callbacks registered it.
		return false, nil
	}

	if fb.executorRegistered {
		// The last local executor left. With no other daprd of the app
		// having one, the rule says to keep hosting: turn into a fallback
		// host in place, so the workflow actor type is never left without
		// a host. This takes no settle period: the daprd hosts the types
		// already, and giving them up meanwhile would leave the app with no
		// host. Otherwise give the types up at once, as without the
		// feature: another daprd has an executor, and hosts them. Until
		// the placement tables have shown the other daprds, give them up
		// too: a fallback host made on that unknown answer would keep its
		// share of the app's instances while no daprd runs them, should one
		// have an executor. A failed switch is retried as it is, so the
		// types are not given up meanwhile.
		if wfe.fallbackAllowed() && want {
			if err := wfe.backend.SetLocalExecutor(ctx, fb.factoryCtx, false); err != nil {
				return false, err
			}
			fb.executorRegistered = false
			log.Infof("The last workflow executor of this daprd left and no other daprd of app '%s' has one; hosting the workflow actor types as a fallback host", wfe.appID)
			return false, nil
		}
		log.Debug("Unregistering workflow actors")
		return false, wfe.releaseActors(ctx)
	}

	if !wfe.fallbackAllowed() || wfe.actorsRegistered == want {
		return false, nil
	}
	if !held {
		return true, nil
	}

	if want {
		log.Infof("No daprd of app '%s' has had a workflow executor for %s; hosting the workflow actor types as a fallback host", wfe.appID, fb.settle)
		wfe.syncExecutorAvailable()
		if err := wfe.backend.RegisterHostedActors(ctx, fb.factoryCtx, false); err != nil {
			return false, err
		}
		wfe.actorsRegistered = true
		return false, nil
	}

	log.Infof("Another daprd of app '%s' has had a workflow executor for %s; this daprd is no longer a fallback host", wfe.appID, fb.damping)
	// A turn on a fallback host fails at once, so the halt waits on no
	// workflow code.
	return false, wfe.releaseActors(ctx)
}

// releaseActors unregisters the workflow actor types. While runFallback runs
// they are drained: the calls placement routes here until the next
// dissemination round moves the instances are still served. Otherwise, as at
// shutdown, they are unregistered at once, as the placement stream is going
// away too. The registration state is reset even if that fails: see
// onWorkItemDisconnection. Must be called with actorRegLock held.
func (wfe *engine) releaseActors(ctx context.Context) error {
	var err error
	if wfe.fallbackAllowed() {
		err = wfe.backend.DrainActors(ctx)
	} else {
		err = wfe.backend.UnRegisterActors(ctx)
	}
	wfe.actorsRegistered = false
	wfe.fallback.executorRegistered = false
	return err
}

// runFallback runs the WorkflowsStagedOperations preview for Run: it follows
// whether another daprd of the app has an executor and applies the rule to
// each change, and each time a local executor connects or leaves, until ctx
// is done. The actor runtime is ready when it is called, so a registration
// does not block worker connections behind actorRegLock.
//
// The watch fails, or its channel closes, only once the actor runtime shuts
// down; runFallback then returns, and the connection callbacks act alone, as
// without the feature. While the placement stream is down, the watch keeps
// its last answer. Before any answer, this daprd does not become a fallback
// host.
func (wfe *engine) runFallback(ctx context.Context) {
	fb := wfe.fallback

	ctx, cancel := context.WithCancel(ctx)
	// Cancelled before stopFallback, so a fallback host is released and no
	// fallback registration is made again, whichever way this returns.
	defer wfe.stopFallback()
	defer cancel()

	wfe.actorRegLock.Lock()
	fb.runCtx = ctx
	wfe.actorRegLock.Unlock()

	presence, err := fb.watch(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Errorf("Failed to watch the workflow executors of app '%s'; the workflow actor types are hosted only with a local executor: %s", wfe.appID, err)
		}
		return
	}

	var (
		remote, remoteKnown bool

		// hold is armed while the hosting the rule wants, holdWant, differs
		// from this daprd's, until it has held for its period: the settle
		// period to become a fallback host, the damping period to give the
		// types up. held is set once it has, until the change is applied.
		hold     clock.Timer
		holdWant bool
		held     bool

		retry <-chan time.Time
	)
	stopHold := func() {
		if hold != nil {
			hold.Stop()
			hold = nil
		}
		held = false
	}
	defer stopHold()

	for {
		var holdC <-chan time.Time
		if hold != nil {
			holdC = hold.C()
		}

		select {
		case <-ctx.Done():
			return
		case r, ok := <-presence:
			if !ok {
				if ctx.Err() == nil {
					log.Errorf("The watch of the workflow executors of app '%s' closed; the workflow actor types are hosted only with a local executor", wfe.appID)
				}
				return
			}
			remote, remoteKnown = r, true
		case <-fb.kick:
		case <-holdC:
			hold = nil
			held = true
		case <-retry:
		}
		retry = nil

		want := remoteKnown && !remote
		if want != holdWant {
			stopHold()
		}

		wfe.actorRegLock.Lock()
		wait, err := wfe.reconcileFallback(ctx, want, held)
		wfe.actorRegLock.Unlock()

		switch {
		case err != nil:
			log.Warnf("Failed to apply the fallback hosting of the workflow actor types, retrying in %s: %s", fallbackRetryInterval, err)
			retry = fb.clock.After(fallbackRetryInterval)
		case !wait:
			stopHold()
		case hold == nil && !held:
			holdWant = want
			if want {
				log.Debugf("No other daprd of app '%s' has a workflow executor; hosting the workflow actor types as a fallback host if none has one in %s", wfe.appID, fb.settle)
				hold = fb.clock.NewTimer(fb.settle)
			} else {
				log.Debugf("Another daprd of app '%s' has a workflow executor; leaving the workflow actor types to it if it still has one in %s", wfe.appID, fb.damping)
				hold = fb.clock.NewTimer(fb.damping)
			}
		}
	}
}

// stopFallback ends the fallback hosting once runFallback's context is done:
// a daprd without a local executor gives the types up, so the app's other
// daprds take its instances over while it shuts down. A daprd with an
// executor unregisters when its executors leave, as without the feature. A
// sweep in flight releases on its own path once it settled.
func (wfe *engine) stopFallback() {
	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()

	if !wfe.actorsRegistered || wfe.localExecutor() || wfe.sweeping > 0 {
		return
	}

	log.Debug("Unregistering the workflow actors of a fallback host")
	if err := wfe.releaseActors(context.Background()); err != nil {
		log.Warnf("Failed to unregister the workflow actors of a fallback host: %s", err)
	}
}
