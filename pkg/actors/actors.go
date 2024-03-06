/*
Copyright 2021 The Dapr Authors
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

package actors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphadose/haxmap"
	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/health"
	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/actors/timers"
	"github.com/dapr/dapr/pkg/channel"
	configuration "github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	daprSeparator        = "||"
	metadataPartitionKey = "partitionKey"

	errStateStoreNotFound      = "actors: state store does not exist or incorrectly configured"
	errStateStoreNotConfigured = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

var (
	log = logger.NewLogger("dapr.runtime.actor")

	ErrIncompatibleStateStore        = errors.New("actor state store does not exist, or does not support transactions which are required to save state - please see https://docs.dapr.io/operations/components/setup-state-store/supported-state-stores/")
	ErrReminderOpActorNotHosted      = errors.New("operations on actor reminders are only possible on hosted actor types")
	ErrTransactionsTooManyOperations = errors.New("the transaction contains more operations than supported by the state store")
	ErrReminderCanceled              = internal.ErrReminderCanceled
)

// ActorRuntime is the main runtime for the actors subsystem.
type ActorRuntime interface {
	Actors
	io.Closer
	Init(context.Context) error
	IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool
	GetRuntimeStatus(ctx context.Context) *runtimev1pb.ActorRuntime
	RegisterInternalActor(ctx context.Context, actorType string, actor InternalActorFactory, actorIdleTimeout time.Duration) error
}

// Actors allow calling into virtual actors as well as actor state management.
type Actors interface {
	// Call an actor.
	Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	// GetState retrieves actor state.
	GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error)
	// GetBulkState retrieves actor state in bulk.
	GetBulkState(ctx context.Context, req *GetBulkStateRequest) (BulkStateResponse, error)
	// TransactionalStateOperation performs a transactional state operation with the actor state store.
	TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) error
	// GetReminder retrieves an actor reminder.
	GetReminder(ctx context.Context, req *GetReminderRequest) (*internal.Reminder, error)
	// CreateReminder creates an actor reminder.
	CreateReminder(ctx context.Context, req *CreateReminderRequest) error
	// DeleteReminder deletes an actor reminder.
	DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error
	// CreateTimer creates an actor timer.
	CreateTimer(ctx context.Context, req *CreateTimerRequest) error
	// DeleteTimer deletes an actor timer.
	DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error
}

// GRPCConnectionFn is the type of the function that returns a gRPC connection
type GRPCConnectionFn func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)

type actorsRuntime struct {
	appChannel         channel.AppChannel
	placement          internal.PlacementService
	placementEnabled   bool
	grpcConnectionFn   GRPCConnectionFn
	actorsConfig       Config
	timers             internal.TimersProvider
	actorsReminders    internal.RemindersProvider
	actorsTable        *sync.Map
	tracingSpec        configuration.TracingSpec
	resiliency         resiliency.Provider
	storeName          string
	compStore          *compstore.ComponentStore
	clock              clock.WithTicker
	internalActorTypes *haxmap.Map[string, InternalActorFactory]
	internalActors     *haxmap.Map[string, InternalActor]
	sec                security.Handler
	checker            *health.Checker
	wg                 sync.WaitGroup
	closed             atomic.Bool
	closeCh            chan struct{}
	apiLevel           atomic.Uint32

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool
}

// ActorsOpts contains options for NewActors.
type ActorsOpts struct {
	AppChannel       channel.AppChannel
	GRPCConnectionFn GRPCConnectionFn
	Config           Config
	TracingSpec      configuration.TracingSpec
	Resiliency       resiliency.Provider
	StateStoreName   string
	CompStore        *compstore.ComponentStore
	Security         security.Handler

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	StateTTLEnabled bool

	// MockPlacement is a placement service implementation used for testing
	MockPlacement internal.PlacementService
}

// NewActors create a new actors runtime with given config.
func NewActors(opts ActorsOpts) (ActorRuntime, error) {
	return newActorsWithClock(opts, &clock.RealClock{})
}

func newActorsWithClock(opts ActorsOpts, clock clock.WithTicker) (ActorRuntime, error) {
	a := &actorsRuntime{
		appChannel:         opts.AppChannel,
		grpcConnectionFn:   opts.GRPCConnectionFn,
		actorsConfig:       opts.Config,
		timers:             timers.NewTimersProvider(clock),
		tracingSpec:        opts.TracingSpec,
		resiliency:         opts.Resiliency,
		storeName:          opts.StateStoreName,
		placement:          opts.MockPlacement,
		actorsTable:        &sync.Map{},
		clock:              clock,
		internalActorTypes: haxmap.New[string, InternalActorFactory](4), // Initial capacity should be enough for the current built-in actors
		internalActors:     haxmap.New[string, InternalActor](32),
		compStore:          opts.CompStore,
		sec:                opts.Security,

		// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
		stateTTLEnabled: opts.StateTTLEnabled,
		closeCh:         make(chan struct{}),
	}

	// Init reminders and placement
	providerOpts := internal.ActorsProviderOptions{
		Config:   a.actorsConfig.Config,
		Security: a.sec,
		AppHealthFn: func(ctx context.Context) <-chan bool {
			if a.checker == nil {
				return nil
			}
			return a.checker.HealthChannel()
		},
		Clock:      a.clock,
		APILevel:   &a.apiLevel,
		Resiliency: a.resiliency,
	}

	// Initialize the placement client if we don't have a mocked one already
	if a.placement == nil {
		factory, fErr := opts.Config.GetPlacementProvider()
		if fErr != nil {
			return nil, fmt.Errorf("failed to initialize placement provider: %w", fErr)
		}
		a.placement = factory(providerOpts)
	}

	factory, err := opts.Config.GetRemindersProvider(a.placement)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize reminders provider: %w", err)
	}
	a.actorsReminders = factory(providerOpts)

	a.actorsReminders.SetExecuteReminderFn(a.executeReminder)
	a.actorsReminders.SetStateStoreProviderFn(a.stateStore)
	a.actorsReminders.SetLookupActorFn(a.isActorLocallyHosted)

	a.placement.SetHaltActorFns(a.haltActor, a.haltAllActors)
	a.placement.SetOnAPILevelUpdate(func(apiLevel uint32) {
		a.apiLevel.Store(apiLevel)
		log.Infof("Actor API level in the cluster has been updated to %d", apiLevel)
	})

	a.timers.SetExecuteTimerFn(a.executeTimer)

	return a, nil
}

func (a *actorsRuntime) isActorLocallyHosted(ctx context.Context, actorType string, actorID string) (isLocal bool, actorAddress string) {
	lar, err := a.placement.LookupActor(ctx, internal.LookupActorRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})
	if err != nil {
		log.Warn(err.Error())
		return false, ""
	}

	if a.isActorLocal(lar.Address, a.actorsConfig.Config.HostAddress, a.actorsConfig.Config.Port) {
		return true, lar.Address
	}
	return false, lar.Address
}

func (a *actorsRuntime) haveCompatibleStorage() bool {
	store, ok := a.compStore.GetStateStore(a.storeName)
	if !ok {
		// If we have hosted actors and no store, we can't initialize the actor runtime
		return false
	}

	features := store.Features()
	return state.FeatureETag.IsPresent(features) && state.FeatureTransactional.IsPresent(features)
}

func (a *actorsRuntime) Init(ctx context.Context) (err error) {
	if a.closed.Load() {
		return errors.New("actors runtime has already been closed")
	}

	if len(a.actorsConfig.ActorsService) == 0 {
		return errors.New("actors: couldn't connect to actors service: address is empty")
	}

	hat := a.actorsConfig.Config.HostedActorTypes.ListActorTypes()
	if len(hat) > 0 {
		if !a.haveCompatibleStorage() {
			return ErrIncompatibleStateStore
		}
	}

	a.actorsReminders.Init(ctx)
	a.timers.Init(ctx)

	a.placementEnabled = true

	a.placement.SetOnTableUpdateFn(func() {
		a.drainRebalancedActors()
		a.actorsReminders.OnPlacementTablesUpdated(ctx)
	})

	a.checker, err = a.getAppHealthChecker()
	if err != nil {
		return fmt.Errorf("actors: couldn't create health check: %w", err)
	}

	if a.checker != nil {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.checker.Run(ctx)
		}()
	}

	for _, actorType := range hat {
		err = a.placement.AddHostedActorType(actorType, a.actorsConfig.GetIdleTimeoutForType(actorType))
		if err != nil {
			return fmt.Errorf("failed to register actor '%s': %w", actorType, err)
		}
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.placement.Start(ctx)
	}()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.deactivationTicker(a.actorsConfig, a.haltActor)
	}()

	log.Infof("Actor runtime started. Actor idle timeout: %v. Actor scan interval: %v",
		a.actorsConfig.Config.ActorIdleTimeout, a.actorsConfig.Config.ActorDeactivationScanInterval)

	return nil
}

func (a *actorsRuntime) getAppHealthChecker() (*health.Checker, error) {
	if len(a.actorsConfig.Config.HostedActorTypes.ListActorTypes()) == 0 || a.appChannel == nil {
		return nil, nil
	}

	// Be careful to configure healthz endpoint option. If app healthz returns unhealthy status, Dapr will
	// disconnect from placement to remove the node from consistent hashing ring.
	// i.e if app is busy state, the healthz status would be flaky, which leads to frequent
	// actor rebalancing. It will impact the entire service.
	return a.getAppHealthCheckerWithOptions(
		health.WithFailureThreshold(4),
		health.WithHealthyStateInterval(5*time.Second),
		health.WithUnHealthyStateInterval(time.Second/2),
		health.WithRequestTimeout(2*time.Second),
		health.WithHTTPClient(a.actorsConfig.HealthHTTPClient),
	)
}

func (a *actorsRuntime) getAppHealthCheckerWithOptions(opts ...health.Option) (*health.Checker, error) {
	opts = append(opts, health.WithAddress(a.actorsConfig.HealthEndpoint+"/healthz"))
	return health.New(opts...)
}

func constructCompositeKey(keys ...string) string {
	return strings.Join(keys, daprSeparator)
}

// Halts an actor, removing it from the actors table and then deactivating it
func (a *actorsRuntime) haltActor(actorType, actorID string) error {
	key := constructCompositeKey(actorType, actorID)
	log.Debugf("Halting actor '%s'", key)

	// Remove the actor from the table
	// This will forbit more state changes
	actAny, ok := a.actorsTable.LoadAndDelete(key)

	// If nothing was loaded, the actor was probably already deactivated
	if !ok || actAny == nil {
		return nil
	}

	act := actAny.(*actor)
	for {
		// wait until actor is not busy, then deactivate
		if !act.isBusy() {
			break
		}

		a.clock.Sleep(time.Millisecond * 100)
	}

	return a.deactivateActor(act)
}

// Halts all actors
func (a *actorsRuntime) haltAllActors() error {
	// Visit all currently active actors and deactivate them
	errCh := make(chan error)
	count := atomic.Int32{}
	a.actorsTable.Range(func(key any, value any) bool {
		count.Add(1)
		go func(key any) {
			actorKey := key.(string)
			err := a.haltActor(a.getActorTypeAndIDFromKey(actorKey))
			if err != nil {
				errCh <- fmt.Errorf("failed to deactivate actor '%s': %v", actorKey, err)
			} else {
				errCh <- nil
			}
		}(key)
		return true
	})

	// Collect all errors, which also waits for all goroutines to return
	errs := []error{}
	for i := int32(0); i < count.Load(); i++ {
		err := <-errCh
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (a *actorsRuntime) deactivateActor(act *actor) error {
	// This uses a background context as it should be unrelated from the caller's context
	// Once the decision to deactivate an actor has been made, we must go through with it or we could have an inconsistent state
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		defer cancel()
		select {
		case <-ctx.Done():
		case <-a.closeCh:
		}
	}()

	// Delete the actor from the actor table regardless of the outcome of deactivation the actor in the app
	actorKey := act.Key()
	a.actorsTable.Delete(actorKey)

	err := a.placement.ReportActorDeactivation(ctx, act.actorType, act.actorID)
	if err != nil {
		return fmt.Errorf("failed to report actor deactivation for actor '%s': %w", actorKey, err)
	}

	// If it's an internal actor, we call it directly
	_, ok := a.internalActorTypes.Get(act.actorType)
	if ok {
		internalAct, loaded := a.internalActors.GetAndDel(act.Key())
		// If the actor was loaded in-memory, call DeactivateActor on it
		if loaded {
			err = internalAct.DeactivateActor(ctx)
			if err != nil {
				diag.DefaultMonitoring.ActorDeactivationFailed(act.actorType, "internal")
				return fmt.Errorf("failed to deactivate internal actor: %w", err)
			}
		}
	} else if a.appChannel != nil {
		req := invokev1.NewInvokeMethodRequest("actors/"+act.actorType+"/"+act.actorID).
			WithActor(act.actorType, act.actorID).
			WithHTTPExtension(http.MethodDelete, "").
			WithContentType(invokev1.JSONContentType)
		defer req.Close()

		resp, err := a.appChannel.InvokeMethod(ctx, req, "")
		if err != nil {
			diag.DefaultMonitoring.ActorDeactivationFailed(act.actorType, "invoke")
			return err
		}
		defer resp.Close()

		if resp.Status().GetCode() != http.StatusOK {
			diag.DefaultMonitoring.ActorDeactivationFailed(act.actorType, "status_code_"+strconv.FormatInt(int64(resp.Status().GetCode()), 10))
			body, _ := resp.RawDataFull()
			return fmt.Errorf("error from actor service: %s", string(body))
		}
	}

	diag.DefaultMonitoring.ActorDeactivated(act.actorType)
	log.Debugf("Deactivated actor '%s'", actorKey)

	return nil
}

func (a *actorsRuntime) removeActorFromTable(actorType, actorID string) {
	a.actorsTable.Delete(constructCompositeKey(actorType, actorID))
}

func (a *actorsRuntime) getActorTypeAndIDFromKey(key string) (string, string) {
	typ, id, _ := strings.Cut(key, daprSeparator)
	return typ, id
}

func (a *actorsRuntime) deactivationTicker(configuration Config, haltFn internal.HaltActorFn) {
	ticker := a.clock.NewTicker(configuration.ActorDeactivationScanInterval)
	ch := ticker.C()
	defer ticker.Stop()

	for {
		select {
		case t := <-ch:
			a.actorsTable.Range(func(key, value any) bool {
				actorInstance := value.(*actor)

				if actorInstance.isBusy() {
					return true
				}

				if !t.Before(actorInstance.ScheduledTime()) {
					a.wg.Add(1)
					go func(actorKey string) {
						defer a.wg.Done()
						actorType, actorID := a.getActorTypeAndIDFromKey(actorKey)
						err := haltFn(actorType, actorID)
						if err != nil {
							log.Errorf("failed to deactivate actor %s: %s", actorKey, err)
						}
					}(key.(string))
				}

				return true
			})
		case <-a.closeCh:
			return
		}
	}
}

// Returns an internal actor instance, allocating it if needed.
// If the actor type does not correspond to an internal actor, the returned boolean is false
func (a *actorsRuntime) getInternalActor(actorType string, actorID string) (InternalActor, bool) {
	factory, ok := a.internalActorTypes.Get(actorType)
	if !ok {
		return nil, false
	}

	internalAct, _ := a.internalActors.GetOrCompute(actorType+daprSeparator+actorID, func() InternalActor {
		return factory(actorType, actorID, a)
	})
	return internalAct, true
}

func (a *actorsRuntime) Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (res *internalv1pb.InternalInvokeResponse, err error) {
	err = a.placement.WaitUntilReady(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for placement readiness: %w", err)
	}

	// Do a lookup to check if the actor is local
	actor := req.GetActor()
	actorType := actor.GetActorType()
	lar, err := a.placement.LookupActor(ctx, internal.LookupActorRequest{
		ActorType: actorType,
		ActorID:   actor.GetActorId(),
	})
	if err != nil {
		return nil, err
	}

	if a.isActorLocal(lar.Address, a.actorsConfig.Config.HostAddress, a.actorsConfig.Config.Port) {
		// If this is an internal actor, we call it using a separate path
		internalAct, ok := a.getInternalActor(actorType, actor.GetActorId())
		if ok {
			res, err = a.callInternalActor(ctx, req, internalAct)
		} else {
			res, err = a.callLocalActor(ctx, req)
		}
	} else {
		res, err = a.callRemoteActorWithRetry(ctx, retry.DefaultLinearRetryCount, retry.DefaultLinearBackoffInterval, a.callRemoteActor, lar.Address, lar.AppID, req)
	}

	if err != nil {
		if res != nil && actorerrors.Is(err) {
			return res, err
		}
		return nil, err
	}
	return res, nil
}

// callRemoteActorWithRetry will call a remote actor for the specified number of retries and will only retry in the case of transient failures.
func (a *actorsRuntime) callRemoteActorWithRetry(
	ctx context.Context,
	numRetries int,
	backoffInterval time.Duration,
	fn func(ctx context.Context, targetAddress, targetID string, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, func(destroy bool), error),
	targetAddress, targetID string, req *internalv1pb.InternalInvokeRequest,
) (*internalv1pb.InternalInvokeResponse, error) {
	if !a.resiliency.PolicyDefined(req.GetActor().GetActorType(), resiliency.ActorPolicy{}) {
		policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, a.resiliency.BuiltInPolicy(resiliency.BuiltInActorRetries))
		return policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
			attempt := resiliency.GetAttempt(ctx)
			rResp, teardown, rErr := fn(ctx, targetAddress, targetID, req)
			if rErr == nil {
				teardown(false)
				return rResp, nil
			}

			code := status.Code(rErr)
			if code == codes.Unavailable || code == codes.Unauthenticated {
				// Destroy the connection and force a re-connection on the next attempt
				teardown(true)
				return rResp, fmt.Errorf("failed to invoke target %s after %d retries. Error: %w", targetAddress, attempt-1, rErr)
			}

			teardown(false)
			return rResp, backoff.Permanent(rErr)
		})
	}

	res, teardown, err := fn(ctx, targetAddress, targetID, req)
	teardown(false)
	return res, err
}

func (a *actorsRuntime) getOrCreateActor(act *internalv1pb.Actor) *actor {
	key := act.GetActorKey()

	// This avoids allocating multiple actor allocations by calling newActor
	// whenever actor is invoked. When storing actor key first, there is a chance to
	// call newActor, but this is trivial.
	val, ok := a.actorsTable.Load(key)
	if !ok {
		actorInstance := newActor(
			act.GetActorType(), act.GetActorId(),
			a.actorsConfig.GetReentrancyForType(act.GetActorType()).MaxStackDepth,
			a.actorsConfig.GetIdleTimeoutForType(act.GetActorType()),
			a.clock,
		)
		val, _ = a.actorsTable.LoadOrStore(key, actorInstance)
	}

	return val.(*actor)
}

func (a *actorsRuntime) callLocalActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	act := a.getOrCreateActor(req.GetActor())

	// Create the InvokeMethodRequest
	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	// Reentrancy to determine how we lock.
	var reentrancyID *string
	if a.actorsConfig.GetReentrancyForType(act.actorType).Enabled {
		if md := imReq.Metadata()["Dapr-Reentrancy-Id"]; md != nil && len(md.GetValues()) > 0 {
			reentrancyID = ptr.Of(md.GetValues()[0])
		} else {
			var uuidObj uuid.UUID
			uuidObj, err = uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("failed to generate UUID: %w", err)
			}
			uuidStr := uuidObj.String()
			imReq.AddMetadata(map[string][]string{
				"Dapr-Reentrancy-Id": {uuidStr},
			})
			reentrancyID = &uuidStr
		}
	}

	err = act.lock(reentrancyID)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	defer act.unlock()

	// Replace method to actors method.
	msg := imReq.Message()
	originalMethod := msg.GetMethod()
	msg.Method = "actors/" + act.actorType + "/" + act.actorID + "/method/" + msg.GetMethod()

	// Reset the method so we can perform retries.
	defer func() {
		msg.Method = originalMethod
	}()

	// Per API contract, actor invocations over HTTP always use PUT as request method
	if msg.GetHttpExtension() == nil {
		imReq.WithHTTPExtension(http.MethodPut, "")
	} else {
		msg.HttpExtension.Verb = commonv1pb.HTTPExtension_PUT //nolint:nosnakecase
	}

	if a.appChannel == nil {
		return nil, fmt.Errorf("app channel for actor type %s is nil", act.actorType)
	}

	policyDef := a.resiliency.ActorPostLockPolicy(act.actorType, act.actorID)

	// If the request can be retried, we need to enable replaying
	if policyDef != nil && policyDef.HasRetries() {
		imReq.WithReplay(true)
	}

	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	imRes, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.appChannel.InvokeMethod(ctx, imReq, "")
	})
	if err != nil {
		return nil, err
	}

	if imRes == nil {
		return nil, errors.New("error from actor service: response object is nil")
	}
	defer imRes.Close()

	if imRes.Status().GetCode() != http.StatusOK {
		respData, _ := imRes.RawDataFull()
		return nil, fmt.Errorf("error from actor service: %s", string(respData))
	}

	// Get the protobuf
	res, err := imRes.ProtoWithData()
	if err != nil {
		return nil, fmt.Errorf("failed to read response data: %w", err)
	}

	// The .NET SDK indicates Actor failure via a header instead of a bad response
	if _, ok := res.GetHeaders()["X-Daprerrorresponseheader"]; ok {
		return res, actorerrors.NewActorError(res)
	}

	return res, nil
}

// Locks an internal actor for a request
//
//nolint:protogetter
func (a *actorsRuntime) lockInternalActorForRequest(act *actor, req *internalv1pb.InternalInvokeRequest) (md map[string][]string, err error) {
	var reentrancyID *string
	// The req object is nil if this is a request for a reminder or timer
	if req != nil {
		// Get metadata in a map
		// Allocate with an extra 1 capacity to add the Dapr-Reentrancy-Id value if needed
		md = make(map[string][]string, len(req.Metadata)+1)
		for k, v := range req.Metadata {
			vals := v.GetValues()
			if len(vals) == 0 {
				continue
			}
			md[k] = vals
		}
	}

	// Reentrancy to determine how we lock.
	if a.actorsConfig.GetReentrancyForType(act.actorType).Enabled {
		if md != nil && len(md["Dapr-Reentrancy-Id"]) > 0 {
			reentrancyID = ptr.Of(md["Dapr-Reentrancy-Id"][0])
		} else {
			var uuidObj uuid.UUID
			uuidObj, err = uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("failed to generate UUID: %w", err)
			}
			reentrancyID = ptr.Of(uuidObj.String())
			if md == nil {
				md = make(map[string][]string, 1)
			}
			md["Dapr-Reentrancy-Id"] = []string{*reentrancyID}
		}
	}

	err = act.lock(reentrancyID)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}

	return md, nil
}

// Calls a local, internal actor
func (a *actorsRuntime) callInternalActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest, internalAct InternalActor) (*internalv1pb.InternalInvokeResponse, error) {
	if req.GetMessage() == nil {
		return nil, errors.New("message is nil in request")
	}

	// Get the actor, activating it as necessary, and the metadata for the request
	act := a.getOrCreateActor(req.GetActor())
	md, err := a.lockInternalActorForRequest(act, req)
	if err != nil {
		return nil, err
	}
	defer act.unlock()

	msg := req.GetMessage()

	policyDef := a.resiliency.ActorPostLockPolicy(act.actorType, act.actorID)
	policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, policyDef)
	return policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
		resData, err := internalAct.InvokeMethod(ctx, msg.GetMethod(), msg.GetData().GetValue(), md)
		if err != nil {
			return nil, fmt.Errorf("error from internal actor: %w", err)
		}

		return &internalv1pb.InternalInvokeResponse{
			Status: &internalv1pb.Status{
				Code: http.StatusOK,
			},
			Message: &commonv1pb.InvokeResponse{
				Data: &anypb.Any{
					Value: resData,
				},
			},
		}, nil
	})
}

func (a *actorsRuntime) callRemoteActor(
	ctx context.Context,
	targetAddress, targetID string,
	req *internalv1pb.InternalInvokeRequest,
) (*internalv1pb.InternalInvokeResponse, func(destroy bool), error) {
	conn, teardown, err := a.grpcConnectionFn(context.TODO(), targetAddress, targetID, a.actorsConfig.Config.Namespace)
	if err != nil {
		return nil, teardown, err
	}

	span := diagUtils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	res, err := client.CallActor(ctx, req)
	if err != nil {
		return nil, teardown, err
	}
	if len(res.GetHeaders()["X-Daprerrorresponseheader"].GetValues()) > 0 {
		return res, teardown, actorerrors.NewActorError(res)
	}

	return res, teardown, nil
}

func (a *actorsRuntime) isActorLocal(targetActorAddress, hostAddress string, grpcPort int) bool {
	portStr := strconv.Itoa(grpcPort)

	if targetActorAddress == hostAddress+":"+portStr {
		// Easy case when there is a perfect match
		return true
	}

	if isLocalhost(hostAddress) && strings.HasSuffix(targetActorAddress, ":"+portStr) {
		return isLocalhost(targetActorAddress[0 : len(targetActorAddress)-len(portStr)-1])
	}

	return false
}

func isLocalhost(addr string) bool {
	return addr == "localhost" || addr == "127.0.0.1" || addr == "[::1]" || addr == "::1"
}

func (a *actorsRuntime) GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error) {
	storeName, store, err := a.stateStore()
	if err != nil {
		return nil, err
	}

	actorKey := req.ActorKey()
	partitionKey := constructCompositeKey(a.actorsConfig.Config.AppID, actorKey)
	metadata := map[string]string{metadataPartitionKey: partitionKey}

	key := a.constructActorStateKey(actorKey, req.Key)

	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	storeReq := &state.GetRequest{
		Key:      key,
		Metadata: metadata,
	}
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, storeReq)
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return &StateResponse{}, nil
	}

	return &StateResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
	}, nil
}

func (a *actorsRuntime) GetBulkState(ctx context.Context, req *GetBulkStateRequest) (BulkStateResponse, error) {
	storeName, store, err := a.stateStore()
	if err != nil {
		return nil, err
	}

	actorKey := req.ActorKey()
	baseKey := constructCompositeKey(a.actorsConfig.Config.AppID, actorKey)
	metadata := map[string]string{metadataPartitionKey: baseKey}

	bulkReqs := make([]state.GetRequest, len(req.Keys))
	for i, key := range req.Keys {
		bulkReqs[i] = state.GetRequest{
			Key:      a.constructActorStateKey(actorKey, key),
			Metadata: metadata,
		}
	}

	policyRunner := resiliency.NewRunner[[]state.BulkGetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	res, err := policyRunner(func(ctx context.Context) ([]state.BulkGetResponse, error) {
		return store.BulkGet(ctx, bulkReqs, state.BulkGetOpts{})
	})
	if err != nil {
		return nil, err
	}

	// Add the dapr separator to baseKey
	baseKey += daprSeparator

	bulkRes := make(BulkStateResponse, len(res))
	for _, r := range res {
		if r.Error != "" {
			return nil, fmt.Errorf("failed to retrieve key '%s': %s", r.Key, r.Error)
		}

		// Trim the prefix from the key
		bulkRes[strings.TrimPrefix(r.Key, baseKey)] = r.Data
	}

	return bulkRes, nil
}

func (a *actorsRuntime) TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) (err error) {
	operations := make([]state.TransactionalStateOperation, len(req.Operations))
	baseKey := constructCompositeKey(a.actorsConfig.Config.AppID, req.ActorKey())
	metadata := map[string]string{metadataPartitionKey: baseKey}
	baseKey += daprSeparator
	for i, o := range req.Operations {
		operations[i], err = o.StateOperation(baseKey, StateOperationOpts{
			Metadata: metadata,
			// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
			StateTTLEnabled: a.stateTTLEnabled,
		})
		if err != nil {
			return err
		}
	}

	return a.executeStateStoreTransaction(ctx, operations, metadata)
}

func (a *actorsRuntime) executeStateStoreTransaction(ctx context.Context, operations []state.TransactionalStateOperation, metadata map[string]string) error {
	storeName, store, err := a.stateStore()
	if err != nil {
		return err
	}

	if maxMulti, ok := store.(state.TransactionalStoreMultiMaxSize); ok {
		max := maxMulti.MultiMaxSize()
		if max > 0 && len(operations) > max {
			return ErrTransactionsTooManyOperations
		}
	}
	stateReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   metadata,
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.Multi(ctx, stateReq)
	})
	return err
}

func (a *actorsRuntime) IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool {
	key := req.ActorKey()
	policyDef := a.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries)
	policyRunner := resiliency.NewRunner[any](ctx, policyDef)
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		_, exists := a.actorsTable.Load(key)
		if !exists {
			// Error message isn't used - we just need to have an error
			return nil, errors.New("")
		}
		return nil, nil
	})
	return err == nil
}

func (a *actorsRuntime) constructActorStateKey(actorKey, key string) string {
	return constructCompositeKey(a.actorsConfig.Config.AppID, actorKey, key)
}

func (a *actorsRuntime) drainRebalancedActors() {
	// visit all currently active actors.
	var wg sync.WaitGroup

	a.actorsTable.Range(func(key any, value any) bool {
		wg.Add(1)
		go func(key any, value any) {
			defer wg.Done()
			// for each actor, deactivate if no longer hosted locally
			actorKey := key.(string)
			actorType, actorID := a.getActorTypeAndIDFromKey(actorKey)
			lar, _ := a.placement.LookupActor(context.TODO(), internal.LookupActorRequest{
				ActorType: actorType,
				ActorID:   actorID,
			})
			if lar.Address != "" && !a.isActorLocal(lar.Address, a.actorsConfig.Config.HostAddress, a.actorsConfig.Config.Port) {
				// actor has been moved to a different host, deactivate when calls are done cancel any reminders
				// each item in reminders contain a struct with some metadata + the actual reminder struct
				a.actorsReminders.DrainRebalancedReminders(actorType, actorID)

				act := value.(*actor)
				if a.actorsConfig.GetDrainRebalancedActorsForType(actorType) {
					// wait until actor isn't busy or timeout hits
					if act.isBusy() {
						select {
						case <-a.clock.After(a.actorsConfig.Config.DrainOngoingCallTimeout):
							break
						case <-act.channel():
							// if a call comes in from the actor for state changes, that's still allowed
							break
						}
					}
				}

				diag.DefaultMonitoring.ActorRebalanced(actorType)

				err := a.haltActor(actorType, actorID)
				if err != nil {
					log.Errorf("Failed to deactivate actor '%s': %v", actorKey, err)
				}
			}
		}(key, value)
		return true
	})

	wg.Wait()
}

// executeTimer implements timers.ExecuteTimerFn.
func (a *actorsRuntime) executeTimer(reminder *internal.Reminder) bool {
	_, exists := a.actorsTable.Load(reminder.ActorKey())
	if !exists {
		log.Errorf("Could not find active timer %s", reminder.Key())
		return false
	}

	err := a.doExecuteReminderOrTimer(context.TODO(), reminder, true)
	diag.DefaultMonitoring.ActorTimerFired(reminder.ActorType, err == nil)
	if err != nil {
		log.Errorf("error invoking timer on actor %s: %s", reminder.ActorKey(), err)
		// Here we return true even if we have an error because the timer can still trigger again
		return true
	}

	return true
}

// executeReminder implements reminders.ExecuteReminderFn.
func (a *actorsRuntime) executeReminder(reminder *internal.Reminder) bool {
	err := a.doExecuteReminderOrTimer(context.TODO(), reminder, false)
	diag.DefaultMonitoring.ActorReminderFired(reminder.ActorType, err == nil)
	if err != nil {
		if errors.Is(err, ErrReminderCanceled) {
			// The handler is explicitly canceling the timer
			log.Debug("Reminder " + reminder.ActorKey() + " was canceled by the actor")
			return false
		}
		log.Errorf("Error invoking reminder on actor %s: %s", reminder.ActorKey(), err)
	}

	return true
}

// Executes a reminder or timer on an internal actor
func (a *actorsRuntime) doExecuteReminderOrTimerOnInternalActor(ctx context.Context, reminder *internal.Reminder, isTimer bool, internalAct InternalActor) (err error) {
	// Get the actor, activating it as necessary, and the metadata for the request
	act := a.getOrCreateActor(&internalv1pb.Actor{
		ActorType: reminder.ActorType,
		ActorId:   reminder.ActorID,
	})
	md, err := a.lockInternalActorForRequest(act, nil)
	if err != nil {
		return err
	}
	defer act.unlock()

	ir := newInternalActorReminder(reminder)
	if isTimer {
		log.Debugf("Executing timer for internal actor '%s'", reminder.Key())

		err = internalAct.InvokeTimer(ctx, ir, md)
		if err != nil {
			if !errors.Is(err, ErrReminderCanceled) {
				log.Errorf("Error executing timer for internal actor '%s': %v", reminder.Key(), err)
			}
			return err
		}
	} else {
		log.Debugf("Executing reminder for internal actor '%s'", reminder.Key())

		err = internalAct.InvokeReminder(ctx, ir, md)
		if err != nil {
			if !errors.Is(err, ErrReminderCanceled) {
				log.Errorf("Error executing reminder for internal actor '%s': %v", reminder.Key(), err)
			}
			return err
		}
	}

	return nil
}

// Executes a reminder or timer
func (a *actorsRuntime) doExecuteReminderOrTimer(ctx context.Context, reminder *internal.Reminder, isTimer bool) (err error) {
	// Sanity check: make sure the actor is actually locally-hosted
	isLocal, _ := a.isActorLocallyHosted(ctx, reminder.ActorType, reminder.ActorID)
	if !isLocal {
		return errors.New("actor is not locally hosted")
	}

	// If it's an internal actor, we call it directly
	internalAct, ok := a.getInternalActor(reminder.ActorType, reminder.ActorID)
	if ok {
		return a.doExecuteReminderOrTimerOnInternalActor(ctx, reminder, isTimer, internalAct)
	}

	var (
		data         []byte
		logName      string
		invokeMethod string
	)

	if isTimer {
		logName = "timer"
		invokeMethod = "timer/" + reminder.Name
		data, err = json.Marshal(&TimerResponse{
			Callback: reminder.Callback,
			Data:     reminder.Data,
			DueTime:  reminder.DueTime,
			Period:   reminder.Period.String(),
		})
		if err != nil {
			return err
		}
	} else {
		logName = "reminder"
		invokeMethod = "remind/" + reminder.Name
		data, err = json.Marshal(&ReminderResponse{
			DueTime: reminder.DueTime,
			Period:  reminder.Period.String(),
			Data:    reminder.Data,
		})
		if err != nil {
			return err
		}
	}
	policyDef := a.resiliency.ActorPreLockPolicy(reminder.ActorType, reminder.ActorID)

	log.Debug("Executing " + logName + " for actor " + reminder.Key())

	req := internalv1pb.NewInternalInvokeRequest(invokeMethod).
		WithActor(reminder.ActorType, reminder.ActorID).
		WithData(data).
		WithContentType(internalv1pb.JSONContentType)

	policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, policyDef)
	_, err = policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
		return a.callLocalActor(ctx, req)
	})
	if err != nil {
		if !errors.Is(err, ErrReminderCanceled) {
			log.Errorf("Error executing %s for actor %s: %v", logName, reminder.Key(), err)
		}
		return err
	}

	return nil
}

func (a *actorsRuntime) CreateReminder(ctx context.Context, req *CreateReminderRequest) error {
	if !a.actorsConfig.Config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	// Create the new reminder object
	reminder, err := req.NewReminder(a.clock.Now())
	if err != nil {
		return err
	}
	return a.actorsReminders.CreateReminder(ctx, reminder)
}

func (a *actorsRuntime) CreateTimer(ctx context.Context, req *CreateTimerRequest) error {
	_, exists := a.actorsTable.Load(req.ActorKey())
	if !exists {
		return fmt.Errorf("can't create timer for actor %s: actor not activated", req.ActorKey())
	}

	reminder, err := req.NewReminder(a.clock.Now())
	if err != nil {
		return err
	}

	return a.timers.CreateTimer(ctx, reminder)
}

func (a *actorsRuntime) DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error {
	if !a.actorsConfig.Config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	return a.actorsReminders.DeleteReminder(ctx, *req)
}

func (a *actorsRuntime) GetReminder(ctx context.Context, req *GetReminderRequest) (*internal.Reminder, error) {
	if !a.actorsConfig.Config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return nil, ErrReminderOpActorNotHosted
	}

	return a.actorsReminders.GetReminder(ctx, req)
}

func (a *actorsRuntime) DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error {
	return a.timers.DeleteTimer(ctx, req.Key())
}

func (a *actorsRuntime) RegisterInternalActor(ctx context.Context, actorType string, factory InternalActorFactory, actorIdleTimeout time.Duration) error {
	if !a.haveCompatibleStorage() {
		return fmt.Errorf("unable to register internal actor type '%s': %w", actorType, ErrIncompatibleStateStore)
	}

	// Call GetOrSet which returns "existing=true" if the actor type was already registered
	_, existing := a.internalActorTypes.GetOrSet(actorType, factory)
	if existing {
		return fmt.Errorf("actor type '%s' already registered", actorType)
	}

	log.Debugf("Registered internal actor type '%s'", actorType)

	a.actorsConfig.Config.HostedActorTypes.AddActorType(actorType, actorIdleTimeout)

	if a.placementEnabled {
		err := a.placement.AddHostedActorType(actorType, actorIdleTimeout)
		if err != nil {
			return fmt.Errorf("error updating hosted actor types: %w", err)
		}
	}
	return nil
}

func (a *actorsRuntime) GetRuntimeStatus(ctx context.Context) *runtimev1pb.ActorRuntime {
	// Do not populate RuntimeStatus, which will be populated by the runtime
	res := &runtimev1pb.ActorRuntime{
		ActiveActors: a.getActiveActorsCount(ctx),
	}

	if a.placementEnabled {
		res.HostReady = a.placement.PlacementHealthy() && a.haveCompatibleStorage()
		res.Placement = a.placement.StatusMessage()
	}

	return res
}

func (a *actorsRuntime) getActiveActorsCount(ctx context.Context) []*runtimev1pb.ActiveActorsCount {
	actorTypes := a.actorsConfig.Config.HostedActorTypes.ListActorTypes()
	actorCountMap := make(map[string]int32, len(actorTypes))
	for _, actorType := range actorTypes {
		if !isInternalActor(actorType) {
			actorCountMap[actorType] = 0
		}
	}
	a.actorsTable.Range(func(key, value any) bool {
		actorType, _ := a.getActorTypeAndIDFromKey(key.(string))
		if !isInternalActor(actorType) {
			actorCountMap[actorType]++
		}
		return true
	})

	activeActorsCount := make([]*runtimev1pb.ActiveActorsCount, len(actorCountMap))
	n := 0
	for actorType, count := range actorCountMap {
		activeActorsCount[n] = &runtimev1pb.ActiveActorsCount{Type: actorType, Count: count}
		n++
	}

	return activeActorsCount
}

func isInternalActor(actorType string) bool {
	return strings.HasPrefix(actorType, InternalActorTypePrefix)
}

// Stop closes all network connections and resources used in actor runtime.
func (a *actorsRuntime) Close() error {
	defer a.wg.Wait()

	var errs []error
	if a.closed.CompareAndSwap(false, true) {
		close(a.closeCh)
		if a.checker != nil {
			a.checker.Close()
		}
		if a.placement != nil {
			if err := a.placement.Close(); err != nil {
				errs = append(errs, fmt.Errorf("failed to close placement service: %w", err))
			}
		}
	}

	return errors.Join(errs...)
}

// ValidateHostEnvironment validates that actors can be initialized properly given a set of parameters
// And the mode the runtime is operating in.
func ValidateHostEnvironment(mTLSEnabled bool, mode modes.DaprMode, namespace string) error {
	switch mode {
	case modes.KubernetesMode:
		if mTLSEnabled && namespace == "" {
			return errors.New("actors must have a namespace configured when running in Kubernetes mode")
		}
	}
	return nil
}

func (a *actorsRuntime) stateStore() (string, internal.TransactionalStateStore, error) {
	storeS, ok := a.compStore.GetStateStore(a.storeName)
	if !ok {
		return "", nil, errors.New(errStateStoreNotFound)
	}

	store, ok := storeS.(internal.TransactionalStateStore)
	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		return "", nil, errors.New(errStateStoreNotConfigured)
	}

	return a.storeName, store, nil
}
