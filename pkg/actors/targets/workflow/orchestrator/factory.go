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

package orchestrator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/messages"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/crypto/spiffe/signer"
)

func newOrchestrator() *orchestrator {
	return &orchestrator{
		lock: lock.NewStallable(),
	}
}

type Options struct {
	AppID              string
	Namespace          string
	WorkflowActorType  string
	ActivityActorType  string
	RetentionActorType string

	Resiliency       resiliency.Provider
	Actors           actors.Interface
	Scheduler        todo.WorkflowScheduler
	EventSink        EventSink
	ActorTypeBuilder *common.ActorTypeBuilder
	RetentionPolicy  *config.WorkflowStateRetentionPolicy

	// Signer provides cryptographic signing and verification. If nil, history
	// signing is disabled.
	Signer *signer.Signer

	// MaxRequestBodySize is the gRPC server max message size in bytes. The
	// orchestrator stalls workflows whose history payload would exceed this
	// limit on the GetWorkItems stream.
	MaxRequestBodySize int

	// May be nil when the feature is disabled.
	WorkflowAccessPolicies *workflowacl.Holder

	// FastPath gates the WorkflowsFastPath preview feature capabilities as
	// one unit: the detached local wake drive (wake.go), the
	// activity-reminder elision with janitor re-dispatch (redispatch.go),
	// and the in-memory completions fold (fold.go).
	FastPath bool
}

type factory struct {
	appID              string
	namespace          string
	actorType          string
	activityActorType  string
	retentionActorType string

	resiliency             resiliency.Provider
	router                 router.Interface
	reminders              reminders.Interface
	actorState             state.Interface
	placement              placement.Interface
	eventSink              EventSink
	actorTypeBuilder       *common.ActorTypeBuilder
	retentionPolicy        *config.WorkflowStateRetentionPolicy
	signer                 *signer.Signer
	maxRequestBodySize     int
	workflowAccessPolicies *workflowacl.Holder

	scheduler todo.WorkflowScheduler

	deactivateCh chan *orchestrator

	// fastPath and the wake* fields drive the detached local wake
	// goroutines (see wake.go). wakeCtx is factory-owned rather than scoped
	// to the per-stream ctx given to New. HaltAll (which also fires on
	// placement stream churn, not only shutdown) cancels and drains the
	// in-flight wakes, then recreates the context for subsequent
	// activations. wakeLock serializes spawns against that cancel/recreate
	// cycle so the WaitGroup Add never races the Wait.
	fastPath   bool
	wakeLock   sync.Mutex
	wakeCtx    context.Context
	wakeCancel context.CancelFunc
	wakeWG     sync.WaitGroup

	// rootCtx bounds wake-failure escalation goroutines (see wake.go
	// escalate): unlike wakeCtx it survives HaltAll, because a reminder
	// create is host-agnostic and must be able to complete during the
	// placement churn that cancels wakeCtx. escWG is waited nowhere on the
	// churn path; the goroutines are rootCtx+timeout bounded.
	rootCtx context.Context
	escLock sync.Mutex
	escWG   sync.WaitGroup

	table sync.Map
	lock  sync.Mutex

	// selfCallerWarned ensures the "policy lists own appID" warning is only
	// emitted once per factory lifetime instead of on every self-call.
	selfCallerWarned atomic.Bool
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	astate, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}

	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	reminders, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}

	placement, err := opts.Actors.Placement(ctx)
	if err != nil {
		return nil, err
	}

	deactivateCh := make(chan *orchestrator, 100)
	go func() {
		for orchestrator := range deactivateCh {
			orchestrator.Deactivate(ctx)
		}
	}()

	wakeCtx, wakeCancel := context.WithCancel(context.Background())

	return &factory{
		appID:                  opts.AppID,
		namespace:              opts.Namespace,
		actorType:              opts.WorkflowActorType,
		activityActorType:      opts.ActivityActorType,
		retentionActorType:     opts.RetentionActorType,
		resiliency:             opts.Resiliency,
		router:                 router,
		reminders:              reminders,
		actorState:             astate,
		eventSink:              opts.EventSink,
		actorTypeBuilder:       opts.ActorTypeBuilder,
		placement:              placement,
		retentionPolicy:        opts.RetentionPolicy,
		signer:                 opts.Signer,
		maxRequestBodySize:     opts.MaxRequestBodySize,
		workflowAccessPolicies: opts.WorkflowAccessPolicies,
		scheduler:              opts.Scheduler,
		deactivateCh:           deactivateCh,
		fastPath:               opts.FastPath,
		wakeCtx:                wakeCtx,
		wakeCancel:             wakeCancel,
		rootCtx:                ctx,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	o, ok := f.table.Load(actorID)
	if !ok {
		fresh := f.initOrchestrator(newOrchestrator(), actorID)
		o, _ = f.table.LoadOrStore(actorID, fresh)
	}

	return o.(*orchestrator)
}

func (f *factory) initOrchestrator(o any, actorID string) *orchestrator {
	or := o.(*orchestrator)

	or.factory = f
	or.actorID = actorID
	or.closed.Store(false)
	or.janitorAsserted.Store(false)
	or.janitorRedispatched = nil
	or.driveRunning.Store(false)
	or.driveNotify = make(chan struct{}, 1)
	or.lock.Init()

	if or.streamFns == nil {
		or.streamFns = make(map[int64]*streamFn)
	}

	// Always allocate Signing, even when f.signer is nil. The
	// attestation/sign methods on Signing are no-ops when Signer is
	// nil, but Tombstone (called from tombstoneTamperedState on a
	// load-time VerificationError) does not depend on Signer and must
	// work for unsigned workflows that hit metadata-bounds or
	// missing-key tampering.
	or.signing = &signing.Signing{
		Signer:            f.signer,
		Namespace:         f.namespace,
		ActorID:           actorID,
		ActorType:         f.actorType,
		ActivityActorType: f.activityActorType,
		Reminders:         f.reminders,
	}

	or.messages = &messages.Messages{
		AppID:                 f.appID,
		ActorID:               actorID,
		ActorType:             f.actorType,
		Router:                f.router,
		ActorTypeBuilder:      f.actorTypeBuilder,
		Signer:                f.signer,
		FailChildWorkflowTask: or.failChildWorkflowTask,
	}

	// Reset the cache state to force a reload from the state store
	or.state = nil
	or.rstate = nil
	or.ometa = nil

	return or
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	// Cancel detached local wake goroutines BEFORE deactivating: a wake
	// goroutine parked on an actor lock is released by the deactivation, and
	// the cancelled wakeCtx stops it from doing further work. Wait for them
	// only after the deactivation loop so neither side deadlocks.
	f.wakeLock.Lock()
	f.wakeCancel()
	f.wakeLock.Unlock()

	var wg sync.WaitGroup
	errs := slice.New[error]()

	f.table.Range(func(_, o any) bool {
		wg.Add(1)
		go func(o *orchestrator) {
			defer wg.Done()
			errs.Append(o.Deactivate(ctx))
		}(o.(*orchestrator))
		return true
	})

	wg.Wait()
	f.wakeWG.Wait()

	// HaltAll also fires on placement disconnection, after which this
	// factory keeps serving new activations: recreate the wake context so
	// the fast path survives the churn.
	f.wakeLock.Lock()
	f.wakeCtx, f.wakeCancel = context.WithCancel(context.Background())
	f.wakeLock.Unlock()

	return errors.Join(errs.Slice()...)
}

func (f *factory) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	var wg sync.WaitGroup
	errs := slice.New[error]()

	f.table.Range(func(key, o any) bool {
		oo := o.(*orchestrator)
		if fn(&api.LookupActorRequest{
			ActorType: f.actorType,
			ActorID:   oo.actorID,
		}) {
			return true
		}

		wg.Add(1)
		go func(o *orchestrator) {
			defer wg.Done()
			errs.Append(o.Deactivate(ctx))
		}(oo)
		return true
	})

	wg.Wait()

	return errors.Join(errs.Slice()...)
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

func (f *factory) deactivate(orchestrator *orchestrator) {
	if !orchestrator.closed.CompareAndSwap(false, true) {
		return
	}
	f.deactivateCh <- orchestrator
}
