/*
Copyright 2025 The Dapr Authors
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

package activity

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/internal/scheduler"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/claim"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/detached"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/kit/crypto/spiffe/signer"
)

type Options struct {
	AppID             string
	Namespace         string
	ActivityActorType string
	WorkflowActorType string
	Scheduler         todo.ActivityScheduler
	Actors            actors.Interface
	ActorTypeBuilder  *common.ActorTypeBuilder
	// Signer produces activity completion attestations so a receiving
	// parent workflow can cryptographically verify the activity's identity,
	// input, and output. Nil when signing is disabled for this deployment.
	Signer *signer.Signer

	// May be nil when the feature is disabled.
	WorkflowAccessPolicies *workflowacl.Holder

	WorkflowsRemoteActivityReminder bool

	// FastPath drives certified activity executions locally in place of
	// their run-activity reminder (WorkflowsFastPath preview feature).
	FastPath bool

	// Detached runs the work that must outlive a placement churn (the
	// drive-failure escalations and the handed-off result publishes) on the
	// runtime lifetime rather than this registration's. Nil creates one
	// bounded by ctx.
	Detached *detached.Runner

	// ExecutionHeld reports whether the durabletask engine on this host holds
	// a completion registration for the given activity work item (dispatched,
	// completion or abandonment still owed). The stale-claim eviction uses it
	// to tell a live execution from one whose work item was lost (see
	// staleClaim). Nil disables eviction.
	ExecutionHeld func(workflowInstanceID string, taskID int32) bool

	// RegisterResolver registers the owner execution's resolve hook with the
	// engine's completion waiter, which invokes it before releasing the held
	// registration (the stale-claim handshake). Nil when the engine backend
	// does not support it; the resolve then happens on callback receipt.
	RegisterResolver func(workflowInstanceID string, taskID int32, resolve func()) func()
}

type factory struct {
	appID             string
	actorType         string
	workflowActorType string

	// TODO: @joshvanl: remove in the next version.
	workflowsRemoteActivityReminder bool

	router                 router.Interface
	reminders              scheduler.Interface
	placement              placement.Interface
	actorTypeBuilder       *common.ActorTypeBuilder
	workflowAccessPolicies *workflowacl.Holder
	signing                *signing.Signing

	scheduler todo.ActivityScheduler

	table sync.Map
	lock  sync.Mutex

	// executionHeld and staleClaimAfter power the stale-claim eviction (see
	// execute.go). staleClaimAfter is a field only so unit tests can compress
	// the grace; it is set once in New.
	executionHeld    func(workflowInstanceID string, taskID int32) bool
	registerResolver func(workflowInstanceID string, taskID int32, resolve func()) func()
	staleClaimAfter  time.Duration

	// inflight tracks activity executions whose WorkItem is in the durabletask
	// queue or being processed by the SDK, keyed by inflight.Key. Shared by
	// every factory of this actor type (see inflightFor).
	inflight *inflight.Map

	// selfCallerWarned emits the "policy lists own appID" warning once per
	// factory lifetime instead of on every self-call.
	selfCallerWarned atomic.Bool

	// fastPath enables the detached local activity drives (see drive.go) in
	// place of the run-activity reminder fire.
	fastPath bool

	// drives is the churn-scoped runner carrying those local drives: HaltAll
	// (which also fires on placement disconnection) aborts and drains it, then
	// installs a fresh scope because the factory keeps serving new activations
	// afterwards. driveLock guards only the swap; the Runner serializes spawns
	// against its own cancellation. driveCancel is held apart from the Runner
	// because HaltAll aborts parked drives before deactivating and drains them
	// only after.
	driveLock   sync.Mutex
	drives      *detached.Runner
	driveCancel context.CancelFunc

	// rootCtx bounds the claim guard goroutines spawned on placement churn
	// (see spawnClaimGuards).
	rootCtx context.Context

	// detached is the runtime-scoped runner for the host-agnostic work that
	// must survive HaltAll: the drive-failure escalation (see drive.go) and
	// the result publish handed off by an owner whose caller went away (see
	// publish.go).
	detached *detached.Runner

	// claims owns the durable execution-claim guards and gate (see the claim
	// subpackage).
	claims *claim.Guards
}

// inflightMaps keeps one inflight map per activity actor type for the life of
// the process. The workflow engine unregisters and re-registers the actor
// types around every worker reconnect and builds a new factory each time; the
// cached outcomes must outlive that, or a janitor re-dispatch landing after
// the re-registration re-runs a body whose result an old registration's
// publish already delivered (or handed to a result reminder).
var inflightMaps sync.Map

func inflightFor(actorType string) *inflight.Map {
	m, _ := inflightMaps.LoadOrStore(actorType, new(inflight.Map))
	return m.(*inflight.Map)
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	det := opts.Detached
	if det == nil {
		det = detached.New(ctx)
	}

	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	state, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}

	reminders, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}

	sreminders, err := reminders.Scheduler()
	if err != nil {
		return nil, err
	}

	placement, err := opts.Actors.Placement(ctx)
	if err != nil {
		return nil, err
	}

	drives, driveCancel := newDriveScope()

	// A completed claim record must outlive the redispatch a janitor fire
	// can have in flight at the moment of completion, so retention is at
	// least one janitor period.
	claimRetention := max(common.EnvDurationOr(
		"DAPR_WORKFLOW_ACTIVITY_CLAIM_RETENTION",
		InflightCacheTTL,
	), common.JanitorPeriod())

	return &factory{
		appID:            opts.AppID,
		actorType:        opts.ActivityActorType,
		inflight:         inflightFor(opts.ActivityActorType),
		fastPath:         opts.FastPath,
		executionHeld:    opts.ExecutionHeld,
		registerResolver: opts.RegisterResolver,
		staleClaimAfter:  2 * common.JanitorPeriod(),
		claims: claim.New(claim.Options{
			ActorType: opts.ActivityActorType,
			State:     state,
			// Half-period beats give a live guard three misses, not one,
			// before its record reads stale under load.
			HeartbeatEvery: common.JanitorPeriod() / 2,
			Retention:      claimRetention,
			StaleAfter:     2 * common.JanitorPeriod(),
		}),
		drives:                 drives,
		driveCancel:            driveCancel,
		rootCtx:                ctx,
		detached:               det,
		router:                 router,
		reminders:              sreminders,
		scheduler:              opts.Scheduler,
		placement:              placement,
		workflowActorType:      opts.WorkflowActorType,
		actorTypeBuilder:       opts.ActorTypeBuilder,
		workflowAccessPolicies: opts.WorkflowAccessPolicies,

		signing: &signing.Signing{
			Signer:    opts.Signer,
			Namespace: opts.Namespace,
		},

		workflowsRemoteActivityReminder: opts.WorkflowsRemoteActivityReminder,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	a, ok := f.table.Load(actorID)
	if !ok {
		a, _ = f.table.LoadOrStore(actorID, &activity{factory: f, actorID: actorID, lock: lock.New()})
	}

	return a.(*activity)
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	// Abort the local activity drives BEFORE deactivating: a drive parked on
	// an activity actor lock aborts on the cancelled context, and one
	// mid-execution hands its in-flight WorkItem to the runtime-scoped
	// publish watcher before returning. Drain them only after the
	// deactivation loop so neither side deadlocks.
	//
	// HaltAll also fires on placement disconnection, after which this factory
	// keeps serving new activations: install a fresh drive scope, so the fast
	// path survives the churn, and retire the old one.
	f.driveLock.Lock()
	drives, cancel := f.drives, f.driveCancel
	f.drives, f.driveCancel = newDriveScope()
	f.driveLock.Unlock()
	cancel()

	f.table.Range(func(key, val any) bool {
		val.(*activity).Deactivate(ctx)
		return true
	})
	f.table.Clear()

	drives.Close()

	return nil
}

// newDriveScope returns a churn-scoped runner for the local activity drives,
// with the cancel that aborts the drives already parked on it. Deliberately
// not rooted in the registration context: a drive scope is retired and
// replaced on every placement churn, which the registration outlives.
func newDriveScope() (*detached.Runner, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	return detached.New(ctx), cancel
}

// driveScope returns the runner currently carrying local activity drives.
func (f *factory) driveScope() *detached.Runner {
	f.driveLock.Lock()
	defer f.driveLock.Unlock()
	return f.drives
}

func (f *factory) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		if !fn(&api.LookupActorRequest{
			ActorType: f.actorType,
			ActorID:   key.(string),
		}) {
			f.spawnClaimGuards(ctx, key.(string))
			val.(*activity).Deactivate(ctx)
			f.table.Delete(key)
		}
		return true
	})
	return nil
}

func (f *factory) Exists(actorID string) bool {
	_, ok := f.table.Load(actorID)
	return ok
}

func (f *factory) Len() int {
	var count int
	f.table.Range(func(_, _ any) bool { count++; return true })
	return count
}

// spawnClaimGuards spawns a claim guard for every unsettled in-flight claim
// of actorID, from HaltNonHosted (placement churn). Deliberately NOT from
// HaltAll: at shutdown the execution dies with the process and a record
// would only delay the new owner by the staleness grace.
func (f *factory) spawnClaimGuards(ctx context.Context, actorID string) {
	if !f.fastPath {
		return
	}
	prefix := actorID + common.ActivityIDSeparator
	f.inflight.Range(func(key string, call *inflight.Call) bool {
		if key != actorID && !strings.HasPrefix(key, prefix) {
			return true
		}
		if call.Settled() {
			return true
		}
		f.claims.Spawn(ctx, f.rootCtx, actorID, key, call)
		return true
	})
}
