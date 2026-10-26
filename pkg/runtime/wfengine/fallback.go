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

// fallbackRetryInterval is how long the engine waits to try a fallback
// registration, or the watch of the other daprds' executors, again after
// one failed.
const fallbackRetryInterval = time.Second

// fallback is the engine's state of the WorkflowsStagedOperations preview (see
// backends/actors/fallback.go). The rule, which each daprd applies alone:
// host the workflow actor types if this daprd has a local executor, or if no
// other daprd of the app has one. The decision depends only on those two
// facts, never on who hosts the workflow actor type itself, so hosting it
// cannot feed back into the decision. Each direction is damped: a daprd
// which hosts nothing becomes a fallback host only once no other daprd has
// had an executor for the settle period, and a fallback host gives the types
// up only once another daprd has had one for the damping period. All fields
// are guarded by the engine's actorRegLock.
//
// Every daprd of the app must run a version which knows the feature before
// it is enabled: an older daprd with a worker does not register the executor
// actor type, so a daprd with the feature and no worker sees no executor,
// hosts the types as a fallback host for good, and owns a share of the
// app's instances which never run while it does.
type fallback struct {
	// runCtx is Run's context; nil until Run. No fallback registration is
	// made before Run, nor once runCtx is done, so shutdown does not
	// re-register.
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
	executorRegistered bool

	// remote is whether another daprd of the app has an executor, as the
	// placement tables last showed; remoteKnown once they showed it.
	remote      bool
	remoteKnown bool

	// watch subscribes to remote. A field so unit tests can drive it.
	watch func(ctx context.Context) (<-chan bool, error)

	clock   clock.WithDelayedExecution
	damping time.Duration
	// dampTimer is armed while a fallback host waits out the damping period
	// with remote set. dampGen tells a stale expiry apart.
	dampTimer clock.Timer
	dampGen   uint64

	settle time.Duration
	// settleTimer is armed while a daprd which hosts nothing waits out the
	// settle period with no executor elsewhere. settleGen tells a stale
	// expiry apart. settled is set once the period passed, until the types
	// are registered or the condition ends; settledCh then tells runFallback
	// to apply the rule, so a failed registration is retried as any other.
	settleTimer clock.Timer
	settleGen   uint64
	settled     bool
	settledCh   chan struct{}
}

func newFallback(watch func(ctx context.Context) (<-chan bool, error)) *fallback {
	factoryCtx, factoryCancel := context.WithCancel(context.Background())
	return &fallback{
		factoryCtx:    factoryCtx,
		factoryCancel: factoryCancel,
		watch:         watch,
		clock:         clock.RealClock{},
		damping:       fallbackDamping(),
		settle:        fallbackSettle(),
		settledCh:     make(chan struct{}, 1),
	}
}

// localExecutor reports whether a workflow worker or an in-process executor
// is connected to this daprd.
func (wfe *engine) localExecutor() bool {
	return wfe.getWorkItemsCount.Load() > 0 || wfe.mcpRegistrationCount.Load() > 0
}

// fallbackAllowed reports whether a fallback registration may be made: Run's
// context is live. Must be called with actorRegLock held.
func (wfe *engine) fallbackAllowed() bool {
	return wfe.fallback.runCtx != nil && wfe.fallback.runCtx.Err() == nil
}

// fallbackWanted reports whether the rule makes this daprd a fallback host,
// were it to have no local executor: a fallback registration may be made,
// and the placement tables have shown no other daprd of the app with an
// executor. Must be called with actorRegLock held.
func (wfe *engine) fallbackWanted() bool {
	return wfe.fallbackAllowed() && wfe.fallback.remoteKnown && !wfe.fallback.remote
}

// reconcileFallback applies the rule to the current local executor count
// and remote presence. Every transition goes through it, under actorRegLock,
// after the caller updated the counts. A caller whose executor left has
// called syncExecutorAvailable: a last executor leaving has cancelled the
// parked completions by then, before anything here waits on actor turn
// locks. A caller whose executor connected has not: the executor is marked
// available here, once the types are registered with it. Until then a turn
// fails at once, as on a fallback host, so the halts a registration waits
// on (of a factory still draining, or one whose drain just completed) wait
// on no workflow code: a turn queued for the connecting worker could not be
// delivered before its connection callback returns. ctx bounds the wait for
// the actor runtime. Must be called with actorRegLock held.
func (wfe *engine) reconcileFallback(ctx context.Context) error {
	fb := wfe.fallback

	if wfe.localExecutor() {
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
		// Only once the switch landed: a failed one, whose executor count
		// is rolled back, stays a fallback host that must still give the
		// types up when the period ends, or a daprd hosting nothing that
		// must still take them when its settle period ends.
		wfe.stopFallbackDamping()
		wfe.stopFallbackSettle()
		return nil
	}

	if wfe.actorsRegistered && fb.executorRegistered {
		// The last local executor left. With no other daprd of the app
		// having one, the rule says to keep hosting: turn into a fallback
		// host in place, so the workflow actor type is never left without
		// a host. This takes no settle period: the daprd hosts the types
		// already, and giving them up meanwhile would leave the app with no
		// host. Otherwise give the types up at once, as without the
		// feature: another daprd has an executor, and hosts them. Until
		// the placement tables have shown the other daprds, give them up
		// too, as the registration below does: with no watch answering, a
		// fallback host made on that unknown answer would not learn of an
		// executor elsewhere, and would keep its share meanwhile.
		if wfe.fallbackWanted() {
			err := wfe.backend.SetLocalExecutor(ctx, fb.factoryCtx, false)
			if err == nil {
				fb.executorRegistered = false
				log.Infof("The last workflow executor of this daprd left and no other daprd of app '%s' has one; hosting the workflow actor types as a fallback host", wfe.appID)
				return nil
			}
			log.Warnf("Failed to turn into a fallback host for the workflow actor types, unregistering them: %s", err)
			// This daprd hosted the types up to now, so it registers them
			// again below without a settle period.
			fb.settled = true
		}

		log.Debug("Unregistering workflow actors")
		if err := wfe.releaseActors(ctx); err != nil {
			return err
		}
	}

	if !wfe.actorsRegistered {
		if !wfe.fallbackWanted() {
			wfe.stopFallbackSettle()
			return nil
		}
		// Only once no other daprd has had an executor for the whole settle
		// period: the placement tables list the daprds connected so far,
		// and the daprd with the worker may be about to connect.
		if !fb.settled {
			wfe.startFallbackSettle()
			return nil
		}
		log.Infof("No daprd of app '%s' has a workflow executor; hosting the workflow actor types as a fallback host", wfe.appID)
		wfe.syncExecutorAvailable()
		if err := wfe.backend.RegisterHostedActors(ctx, fb.factoryCtx, false); err != nil {
			return err
		}
		wfe.actorsRegistered = true
		wfe.stopFallbackSettle()
		return nil
	}

	// A fallback host. It gives the types up only once another daprd has
	// had an executor for the whole damping period.
	if fb.remote {
		wfe.startFallbackDamping()
	} else {
		wfe.stopFallbackDamping()
	}
	return nil
}

// releaseActors unregisters the workflow actor types. While Run is live they
// are drained: the calls placement routes here until the next dissemination
// round moves the instances are still served. At shutdown they are
// unregistered at once, as the placement stream is going away too. The
// registration state is reset even if that fails: see
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

// startFallbackDamping arms the damping timer of a fallback host, unless it
// is armed: remote has been set since. Must be called with actorRegLock
// held.
func (wfe *engine) startFallbackDamping() {
	fb := wfe.fallback
	if fb.dampTimer != nil {
		return
	}
	fb.dampGen++
	gen := fb.dampGen
	log.Debugf("Another daprd of app '%s' has a workflow executor; leaving the workflow actor types to it if it still has one in %s", wfe.appID, fb.damping)
	fb.dampTimer = fb.clock.AfterFunc(fb.damping, func() {
		wfe.fallbackDampingExpired(gen)
	})
}

// stopFallbackDamping disarms the damping timer. Must be called with
// actorRegLock held.
func (wfe *engine) stopFallbackDamping() {
	fb := wfe.fallback
	if fb.dampTimer == nil {
		return
	}
	fb.dampTimer.Stop()
	fb.dampTimer = nil
	fb.dampGen++
}

// fallbackDampingExpired unregisters the workflow actor types of a fallback
// host once another daprd of the app has had an executor for the damping
// period.
func (wfe *engine) fallbackDampingExpired(gen uint64) {
	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()

	fb := wfe.fallback
	if gen != fb.dampGen {
		return
	}
	fb.dampTimer = nil

	if !wfe.actorsRegistered || fb.executorRegistered || wfe.localExecutor() || !fb.remote {
		return
	}

	log.Infof("Another daprd of app '%s' has had a workflow executor for %s; this daprd is no longer a fallback host", wfe.appID, fb.damping)
	// A turn on a fallback host fails at once, so the halt waits on no
	// workflow code.
	if err := wfe.releaseActors(context.Background()); err != nil {
		log.Warnf("Failed to unregister the workflow actors of a fallback host: %s", err)
	}
}

// startFallbackSettle arms the settle timer of a daprd which hosts nothing,
// unless it is armed or has expired: no other daprd has had an executor
// since. Must be called with actorRegLock held.
func (wfe *engine) startFallbackSettle() {
	fb := wfe.fallback
	if fb.settleTimer != nil || fb.settled {
		return
	}
	fb.settleGen++
	gen := fb.settleGen
	log.Debugf("No other daprd of app '%s' has a workflow executor; hosting the workflow actor types as a fallback host if none has one in %s", wfe.appID, fb.settle)
	fb.settleTimer = fb.clock.AfterFunc(fb.settle, func() {
		wfe.fallbackSettleExpired(gen)
	})
}

// stopFallbackSettle disarms the settle timer, and forgets a settle period
// which passed. Must be called with actorRegLock held.
func (wfe *engine) stopFallbackSettle() {
	fb := wfe.fallback
	fb.settled = false
	if fb.settleTimer == nil {
		return
	}
	fb.settleTimer.Stop()
	fb.settleTimer = nil
	fb.settleGen++
}

// fallbackSettleExpired marks the settle period passed once no other daprd
// of the app has had an executor for it, and has runFallback apply the rule,
// which registers the types as a fallback host.
func (wfe *engine) fallbackSettleExpired(gen uint64) {
	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()

	fb := wfe.fallback
	if gen != fb.settleGen {
		return
	}
	fb.settleTimer = nil
	fb.settled = true

	select {
	case fb.settledCh <- struct{}{}:
	default:
	}
}

// runFallback runs the WorkflowsStagedOperations preview for Run: it follows
// whether another daprd of the app has an executor and applies the rule to
// each change, until ctx is done. The actor runtime is ready when it is
// called, so a registration does not block worker connections behind
// actorRegLock.
func (wfe *engine) runFallback(ctx context.Context) {
	fb := wfe.fallback

	wfe.actorRegLock.Lock()
	fb.runCtx = ctx
	wfe.actorRegLock.Unlock()

	// A watch which failed or closed is set up again after the retry
	// interval until ctx is done; ch is nil meanwhile. remote and remoteKnown
	// keep the last answer, as the watch itself does while the placement
	// stream is down: the new watch answers once a dissemination round has
	// completed on the stream, as the old one would have, so the answer is no
	// staler than with the old watch kept. Before any answer, remoteKnown
	// stays unset, and this daprd does not become a fallback host.
	var ch <-chan bool
	var retry, rewatch <-chan time.Time
	watch := func() {
		var err error
		if ch, err = fb.watch(ctx); err != nil {
			ch = nil
			rewatch = fb.clock.After(fallbackRetryInterval)
			if ctx.Err() == nil {
				log.Warnf("Failed to watch the workflow executors of app '%s', retrying in %s: %s", wfe.appID, fallbackRetryInterval, err)
			}
		}
	}
	watch()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rewatch:
			rewatch = nil
			watch()
			continue
		case <-retry:
			wfe.actorRegLock.Lock()
		case <-fb.settledCh:
			wfe.actorRegLock.Lock()
		case remote, ok := <-ch:
			if !ok {
				if ctx.Err() != nil {
					return
				}
				log.Warnf("The watch of the workflow executors of app '%s' closed, watching again in %s", wfe.appID, fallbackRetryInterval)
				ch = nil
				rewatch = fb.clock.After(fallbackRetryInterval)
				continue
			}
			// The rule is applied under the same lock, so no one sees the
			// new answer before it is applied.
			wfe.actorRegLock.Lock()
			fb.remote = remote
			fb.remoteKnown = true
		}

		retry = nil
		err := wfe.reconcileFallback(ctx)
		wfe.actorRegLock.Unlock()
		if err != nil {
			log.Warnf("Failed to apply the fallback hosting of the workflow actor types, retrying: %s", err)
			retry = fb.clock.After(fallbackRetryInterval)
		}
	}
}

// stopFallback ends the fallback hosting once Run's context is done: a
// fallback host gives the types up, so the app's other daprds take its
// instances over while it shuts down. A daprd with an executor unregisters
// when its executors leave, as without the feature.
func (wfe *engine) stopFallback() {
	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()

	fb := wfe.fallback
	wfe.stopFallbackDamping()
	wfe.stopFallbackSettle()
	if !wfe.actorsRegistered || fb.executorRegistered {
		return
	}

	log.Debug("Unregistering the workflow actors of a fallback host")
	if err := wfe.releaseActors(context.Background()); err != nil {
		log.Warnf("Failed to unregister the workflow actors of a fallback host: %s", err)
	}
}
