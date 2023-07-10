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
	"net"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	core "github.com/dapr/dapr/pkg/actors/core"
	coreReminder "github.com/dapr/dapr/pkg/actors/core/reminder"
	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/channel"
	configuration "github.com/dapr/dapr/pkg/config"
	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

const (
	daprSeparator        = "||"
	metadataPartitionKey = "partitionKey"
	metadataZeroID       = "00000000-0000-0000-0000-000000000000"

	errStateStoreNotFound      = "actors: state store does not exist or incorrectly configured"
	errStateStoreNotConfigured = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

var log = logger.NewLogger("dapr.runtime.actor")

// PlacementService allows for interacting with the actor placement service.
type PlacementService interface {
	Start()
	Stop()
	WaitUntilPlacementTableIsReady(ctx context.Context) error
	LookupActor(actorType, actorID string) (host string, appID string)
	AddHostedActorType(actorType string) error
}

// GRPCConnectionFn is the type of the function that returns a gRPC connection
type GRPCConnectionFn func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)

type actorsRuntime struct {
	appChannel            channel.AppChannel
	placement             PlacementService
	grpcConnectionFn      GRPCConnectionFn
	config                Config
	actorsTable           *sync.Map
	activeTimers          *sync.Map
	activeTimersCount     map[string]*int64
	activeTimersCountLock *sync.RWMutex
	activeTimersLock      *sync.RWMutex
	activeReminders       *sync.Map
	remindersLock         *sync.RWMutex
	remindersStoringLock  *sync.Mutex
	reminders             map[string][]core.ActorReminderReference
	evaluationLock        sync.RWMutex
	evaluationChan        chan struct{}
	appHealthy            *atomic.Bool
	certChain             *daprCredentials.CertChain
	tracingSpec           configuration.TracingSpec
	resiliency            resiliency.Provider
	storeName             string
	compStore             *compstore.ComponentStore
	ctx                   context.Context
	cancel                context.CancelFunc
	clock                 clock.WithTicker
	internalActors        map[string]core.InternalActor
	internalActorChannel  *core.InternalActorChannel
	localActor            *core.LocalActor
	actorsReminders       core.Reminders
	actorsTimers          core.Timers

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool
}

var ErrIncompatibleStateStore = errors.New("actor state store does not exist, or does not support transactions which are required to save state - please see https://docs.dapr.io/operations/components/setup-state-store/supported-state-stores/")

var ErrDaprResponseHeader = errors.New("error indicated via actor header response")

var ErrReminderCanceled = errors.New("reminder has been canceled")

// ActorsOpts contains options for NewActors.
type ActorsOpts struct {
	AppChannel       channel.AppChannel
	GRPCConnectionFn GRPCConnectionFn
	Config           Config
	CertChain        *daprCredentials.CertChain
	TracingSpec      configuration.TracingSpec
	Resiliency       resiliency.Provider
	StateStoreName   string
	CompStore        *compstore.ComponentStore

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	StateTTLEnabled bool

	// MockPlacement is a placement service implementation used for testing
	MockPlacement PlacementService
}

// NewActors create a new actors runtime with given config.
func NewActors(opts ActorsOpts) core.Actors {
	return newActorsWithClock(opts, &clock.RealClock{})
}

func newActorsWithClock(opts ActorsOpts, clock clock.WithTicker) core.Actors {
	appHealthy := &atomic.Bool{}
	appHealthy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	evaluationChan := make(chan struct{}, 1)
	internalActorChannel := core.NewInternalActorChannel()
	actorsTable := &sync.Map{}
	compStore := opts.CompStore
	localActorOpts := core.LocalActorOpts{
		AppChannel:           opts.AppChannel,
		Config:               opts.Config.coreConfig,
		Resiliency:           opts.Resiliency,
		InternalActorChannel: internalActorChannel,
		Clock:                clock,
		ActorsTable:          actorsTable,
	}
	localActor := core.NewLocalActor(localActorOpts, clock)
	remindersMap := map[string][]core.ActorReminderReference{}
	remindersStoringLock := &sync.Mutex{}
	remindersLock := sync.RWMutex{}
	activeReminders := &sync.Map{}
	remindersOpts := reminders.RemindersOpts{
		Resiliency:           &opts.Resiliency,
		StateStoreName:       opts.StateStoreName,
		CallActorFn:          localActor.CallLocalActor,
		Ctx:                  &ctx,
		EvaluationChan:       evaluationChan,
		Config:               &opts.Config.coreConfig,
		Clock:                &clock,
		RemindersStoringLock: remindersStoringLock,
		ActiveReminders:      activeReminders,
		RemindersLock:        &remindersLock,
		Reminders:            remindersMap,
		CompStore:            compStore,
	}
	actorsReminders := reminders.NewReminders(remindersOpts)
	activeTimersLock := sync.RWMutex{}
	activeTimersCountLock := sync.RWMutex{}
	activeTimers := &sync.Map{}
	activeTimersCount := make(map[string]*int64)
	timerOpts := reminders.TimerOpts{
		ActiveTimersLock:      &activeTimersLock,
		Clock:                 &clock,
		ActorsTable:           actorsTable,
		ActiveTimers:          activeTimers,
		ActorsReminders:       actorsReminders,
		ActiveTimersCountLock: &activeTimersCountLock,
		ActiveTimersCount:     activeTimersCount,
	}
	a := actorsRuntime{
		appChannel:            opts.AppChannel,
		grpcConnectionFn:      opts.GRPCConnectionFn,
		config:                opts.Config,
		certChain:             opts.CertChain,
		tracingSpec:           opts.TracingSpec,
		resiliency:            opts.Resiliency,
		storeName:             opts.StateStoreName,
		placement:             opts.MockPlacement,
		actorsTable:           actorsTable,
		activeTimers:          activeTimers,
		activeTimersCount:     activeTimersCount,
		activeReminders:       activeReminders,
		reminders:             remindersMap,
		evaluationChan:        evaluationChan,
		appHealthy:            appHealthy,
		ctx:                   ctx,
		cancel:                cancel,
		clock:                 clock,
		internalActors:        map[string]core.InternalActor{},
		internalActorChannel:  internalActorChannel,
		compStore:             compStore,
		localActor:            localActor,
		remindersStoringLock:  remindersStoringLock,
		remindersLock:         &remindersLock,
		activeTimersLock:      &activeTimersLock,
		activeTimersCountLock: &activeTimersCountLock,
		actorsReminders:       actorsReminders,
		actorsTimers:          reminders.NewTimers(timerOpts),
		// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
		stateTTLEnabled: opts.StateTTLEnabled,
	}
	return &a
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

func (a *actorsRuntime) Init() error {
	if len(a.config.coreConfig.PlacementAddresses) == 0 {
		return errors.New("actors: couldn't connect to placement service: address is empty")
	}

	if len(a.config.coreConfig.HostedActorTypes) > 0 {
		if !a.haveCompatibleStorage() {
			return ErrIncompatibleStateStore
		}
	}
	store, _ := a.stateStore()
	a.actorsReminders.SetStateStore(store)
	hostname := net.JoinHostPort(a.config.coreConfig.HostAddress, strconv.Itoa(a.config.coreConfig.Port))

	afterTableUpdateFn := func() {
		a.drainRebalancedActors()
		a.evaluateReminders(context.TODO())
	}
	appHealthFn := func() bool { return a.appHealthy.Load() }

	if a.placement == nil {
		a.placement = internal.NewActorPlacement(
			a.config.coreConfig.PlacementAddresses, a.certChain,
			a.config.coreConfig.AppID, hostname, a.config.coreConfig.PodName, a.config.coreConfig.HostedActorTypes,
			appHealthFn,
			afterTableUpdateFn)
	}

	go a.placement.Start()
	go a.deactivationTicker(a.config, a.deactivateActor)

	log.Infof("actor runtime started. actor idle timeout: %v. actor scan interval: %v",
		a.config.coreConfig.ActorIdleTimeout, a.config.coreConfig.ActorDeactivationScanInterval)

	// Be careful to configure healthz endpoint option. If app healthz returns unhealthy status, Dapr will
	// disconnect from placement to remove the node from consistent hashing ring.
	// i.e if app is busy state, the healthz status would be flaky, which leads to frequent
	// actor rebalancing. It will impact the entire service.
	go a.startAppHealthCheck(
		health.WithFailureThreshold(4),
		health.WithInterval(5*time.Second),
		health.WithRequestTimeout(2*time.Second),
		health.WithHTTPClient(a.config.coreConfig.HealthHTTPClient),
	)

	return nil
}

func (a *actorsRuntime) startAppHealthCheck(opts ...health.Option) {
	if len(a.config.coreConfig.HostedActorTypes) == 0 || a.appChannel == nil {
		return
	}

	ch := health.StartEndpointHealthCheck(a.ctx, a.config.coreConfig.HealthEndpoint+"/healthz", opts...)
	for {
		select {
		case <-a.ctx.Done():
			break
		case appHealthy := <-ch:
			a.appHealthy.Store(appHealthy)
		}
	}
}

func constructCompositeKey(keys ...string) string {
	return strings.Join(keys, daprSeparator)
}

func (a *actorsRuntime) deactivateActor(actorType, actorID string) error {
	req := invokev1.NewInvokeMethodRequest("actors/"+actorType+"/"+actorID).
		WithActor(actorType, actorID).
		WithHTTPExtension(nethttp.MethodDelete, "").
		WithContentType(invokev1.JSONContentType)
	defer req.Close()

	// TODO Propagate context.
	ctx := context.TODO()

	resp, err := a.localActor.GetAppChannel(actorType).InvokeMethod(ctx, req, "")
	if err != nil {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "invoke")
		return err
	}
	defer resp.Close()

	if resp.Status().Code != nethttp.StatusOK {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "status_code_"+strconv.FormatInt(int64(resp.Status().Code), 10))
		body, _ := resp.RawDataFull()
		return fmt.Errorf("error from actor service: %s", string(body))
	}

	a.removeActorFromTable(actorType, actorID)
	diag.DefaultMonitoring.ActorDeactivated(actorType)
	log.Debugf("deactivated actor type=%s, id=%s\n", actorType, actorID)

	return nil
}

func (a *actorsRuntime) removeActorFromTable(actorType, actorID string) {
	a.actorsTable.Delete(constructCompositeKey(actorType, actorID))
}

func (a *actorsRuntime) getActorTypeAndIDFromKey(key string) (string, string) {
	arr := strings.Split(key, daprSeparator)
	return arr[0], arr[1]
}

type deactivateFn = func(actorType string, actorID string) error

func (a *actorsRuntime) deactivationTicker(configuration Config, deactivateFn deactivateFn) {
	ticker := a.clock.NewTicker(configuration.coreConfig.ActorDeactivationScanInterval)
	ch := ticker.C()
	defer ticker.Stop()

	for {
		select {
		case t := <-ch:
			a.actorsTable.Range(func(key, value interface{}) bool {
				actorInstance := value.(*core.Actor)

				if actorInstance.IsBusy() {
					return true
				}

				durationPassed := t.Sub(actorInstance.LastUsedTime)
				if durationPassed >= configuration.GetIdleTimeoutForType(actorInstance.ActorType) {
					go func(actorKey string) {
						actorType, actorID := a.getActorTypeAndIDFromKey(actorKey)
						err := deactivateFn(actorType, actorID)
						if err != nil {
							log.Errorf("failed to deactivate actor %s: %s", actorKey, err)
						}
					}(key.(string))
				}

				return true
			})
		case <-a.ctx.Done():
			return
		}
	}
}

type lookupActorRes struct {
	targetActorAddress string
	appID              string
}

func (a *actorsRuntime) Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	err := a.placement.WaitUntilPlacementTableIsReady(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for placement table readiness: %w", err)
	}

	actor := req.Actor()
	// Retry here to allow placement table dissemination/rebalancing to happen.
	policyDef := a.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries)
	policyRunner := resiliency.NewRunner[*lookupActorRes](ctx, policyDef)
	lar, err := policyRunner(func(ctx context.Context) (*lookupActorRes, error) {
		rAddr, rAppID := a.placement.LookupActor(actor.GetActorType(), actor.GetActorId())
		if rAddr == "" {
			return nil, fmt.Errorf("error finding address for actor type %s with id %s", actor.GetActorType(), actor.GetActorId())
		}
		return &lookupActorRes{
			targetActorAddress: rAddr,
			appID:              rAppID,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	if lar == nil {
		lar = &lookupActorRes{}
	}
	var resp *invokev1.InvokeMethodResponse
	if a.isActorLocal(lar.targetActorAddress, a.config.coreConfig.HostAddress, a.config.coreConfig.Port) {
		resp, err = a.localActor.CallLocalActor(ctx, req)
	} else {
		resp, err = a.callRemoteActorWithRetry(ctx, retry.DefaultLinearRetryCount, retry.DefaultLinearBackoffInterval, a.callRemoteActor, lar.targetActorAddress, lar.appID, req)
	}

	if err != nil {
		if errors.Is(err, ErrDaprResponseHeader) {
			// We return the response to maintain the .NET Actor contract which communicates errors via the body, but resiliency needs the error to retry.
			return resp, err
		}
		if resp != nil {
			resp.Close()
		}
		return nil, err
	}
	return resp, nil
}

// callRemoteActorWithRetry will call a remote actor for the specified number of retries and will only retry in the case of transient failures.
func (a *actorsRuntime) callRemoteActorWithRetry(
	ctx context.Context,
	numRetries int,
	backoffInterval time.Duration,
	fn func(ctx context.Context, targetAddress, targetID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, func(destroy bool), error),
	targetAddress, targetID string, req *invokev1.InvokeMethodRequest,
) (*invokev1.InvokeMethodResponse, error) {
	if !a.resiliency.PolicyDefined(req.Actor().ActorType, resiliency.ActorPolicy{}) {
		// This policy has built-in retries so enable replay in the request
		req.WithReplay(true)
		policyRunner := resiliency.NewRunnerWithOptions(ctx,
			a.resiliency.BuiltInPolicy(resiliency.BuiltInActorRetries),
			resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
				Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
			},
		)
		attempts := atomic.Int32{}
		return policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
			attempt := attempts.Add(1)
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

	resp, teardown, err := fn(ctx, targetAddress, targetID, req)
	teardown(false)
	return resp, err
}

func (a *actorsRuntime) callRemoteActor(
	ctx context.Context,
	targetAddress, targetID string,
	req *invokev1.InvokeMethodRequest,
) (*invokev1.InvokeMethodResponse, func(destroy bool), error) {
	conn, teardown, err := a.grpcConnectionFn(context.TODO(), targetAddress, targetID, a.config.coreConfig.Namespace)
	if err != nil {
		return nil, teardown, err
	}

	span := diagUtils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	pd, err := req.ProtoWithData()
	if err != nil {
		return nil, teardown, fmt.Errorf("failed to read data from request object: %w", err)
	}
	resp, err := client.CallActor(ctx, pd)
	if err != nil {
		return nil, teardown, err
	}

	invokeResponse, invokeErr := invokev1.InternalInvokeResponse(resp)
	if invokeErr != nil {
		return nil, teardown, invokeErr
	}

	// Generated gRPC client eats the response when we send
	if _, ok := invokeResponse.Headers()["X-Daprerrorresponseheader"]; ok {
		return invokeResponse, teardown, ErrDaprResponseHeader
	}

	return invokeResponse, teardown, nil
}

func (a *actorsRuntime) isActorLocal(targetActorAddress, hostAddress string, grpcPort int) bool {
	return strings.Contains(targetActorAddress, "localhost") || strings.Contains(targetActorAddress, "127.0.0.1") ||
		targetActorAddress == hostAddress+":"+strconv.Itoa(grpcPort)
}

func (a *actorsRuntime) GetState(ctx context.Context, req *coreReminder.GetStateRequest) (*coreReminder.StateResponse, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}
	// store := a.stateStoreS

	actorKey := req.ActorKey()
	partitionKey := constructCompositeKey(a.config.coreConfig.AppID, actorKey)
	metadata := map[string]string{metadataPartitionKey: partitionKey}

	key := a.constructActorStateKey(actorKey, req.Key)

	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
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
		return &coreReminder.StateResponse{}, nil
	}

	return &coreReminder.StateResponse{
		Data: resp.Data,
	}, nil
}

func (a *actorsRuntime) TransactionalStateOperation(ctx context.Context, req *core.TransactionalRequest) error {
	store, err := a.stateStore()
	if err != nil {
		return err
	}
	// store := a.stateStoreS

	operations := make([]state.TransactionalStateOperation, len(req.Operations))
	baseKey := constructCompositeKey(a.config.coreConfig.AppID, req.ActorKey())
	metadata := map[string]string{metadataPartitionKey: baseKey}
	baseKey += daprSeparator
	for i, o := range req.Operations {
		operations[i], err = o.StateOperation(baseKey, core.StateOperationOpts{
			Metadata: metadata,
			// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
			StateTTLEnabled: a.stateTTLEnabled,
		})
		if err != nil {
			return err
		}
	}

	return a.actorsReminders.ExecuteStateStoreTransaction(ctx, store, operations, metadata)
}

func (a *actorsRuntime) IsActorHosted(ctx context.Context, req *coreReminder.ActorHostedRequest) bool {
	key := constructCompositeKey(req.ActorType, req.ActorID)
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
	return constructCompositeKey(a.config.coreConfig.AppID, actorKey, key)
}

func (a *actorsRuntime) drainRebalancedActors() {
	// visit all currently active actors.
	var wg sync.WaitGroup

	a.actorsTable.Range(func(key any, value any) bool {
		wg.Add(1)
		go func(key any, value any, wg *sync.WaitGroup) {
			defer wg.Done()
			// for each actor, deactivate if no longer hosted locally
			actorKey := key.(string)
			actorType, actorID := a.getActorTypeAndIDFromKey(actorKey)
			address, _ := a.placement.LookupActor(actorType, actorID)
			if address != "" && !a.isActorLocal(address, a.config.coreConfig.HostAddress, a.config.coreConfig.Port) {
				// actor has been moved to a different host, deactivate when calls are done cancel any reminders
				// each item in reminders contain a struct with some metadata + the actual reminder struct
				a.remindersLock.RLock()
				reminders := a.reminders[actorType]
				a.remindersLock.RUnlock()
				for _, r := range reminders {
					// r.Reminder refers to the actual reminder struct that is saved in the db
					if r.Reminder.ActorType == actorType && r.Reminder.ActorID == actorID {
						reminderKey := constructCompositeKey(actorKey, r.Reminder.Name)
						stopChan, exists := a.activeReminders.Load(reminderKey)
						if exists {
							close(stopChan.(chan struct{}))
							a.activeReminders.Delete(reminderKey)
						}
					}
				}

				actor := value.(*core.Actor)
				if a.config.GetDrainRebalancedActorsForType(actorType) {
					// wait until actor isn't busy or timeout hits
					if actor.IsBusy() {
						select {
						case <-a.clock.After(a.config.coreConfig.DrainOngoingCallTimeout):
							break
						case <-actor.Channel():
							// if a call comes in from the actor for state changes, that's still allowed
							break
						}
					}
				}

				// don't allow state changes
				a.actorsTable.Delete(key)

				diag.DefaultMonitoring.ActorRebalanced(actorType)

				for {
					// wait until actor is not busy, then deactivate
					if !actor.IsBusy() {
						err := a.deactivateActor(actorType, actorID)
						if err != nil {
							log.Errorf("failed to deactivate actor %s: %s", actorKey, err)
						}
						break
					}
					a.clock.Sleep(time.Millisecond * 500)
				}
			}
		}(key, value, &wg)
		return true
	})

	wg.Wait()
}

func (a *actorsRuntime) evaluateReminders(ctx context.Context) {
	a.evaluationLock.Lock()
	defer a.evaluationLock.Unlock()

	a.evaluationChan <- struct{}{}

	var wg sync.WaitGroup
	for _, t := range a.config.coreConfig.HostedActorTypes {
		vals, _, err := a.getRemindersForActorType(ctx, t, true)
		if err != nil {
			log.Errorf("Error getting reminders for actor type %s: %s", t, err)
			continue
		}

		log.Debugf("Loaded %d reminders for actor type %s", len(vals), t)
		a.remindersLock.Lock()
		a.reminders[t] = vals
		a.remindersLock.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := range vals {
				rmd := vals[i].Reminder
				reminderKey := rmd.Key()
				targetActorAddress, _ := a.placement.LookupActor(rmd.ActorType, rmd.ActorID)
				if targetActorAddress == "" {
					log.Warn("Did not find address for actor for reminder " + reminderKey)
					continue
				}

				if a.isActorLocal(targetActorAddress, a.config.coreConfig.HostAddress, a.config.coreConfig.Port) {
					_, exists := a.activeReminders.Load(reminderKey)

					if !exists {
						stop := make(chan struct{})
						a.activeReminders.Store(reminderKey, stop)
						err := a.actorsReminders.StartReminder(&rmd, stop)
						if err != nil {
							log.Errorf("Error starting reminder %s: %v", reminderKey, err)
						} else {
							log.Debug("Started reminder " + reminderKey)
						}
					} else {
						log.Debug("Reminder " + reminderKey + " already exists")
					}
				} else {
					stopChan, exists := a.activeReminders.Load(reminderKey)
					if exists {
						log.Debugf("Stopping reminder %s on %s as it's active on host %s", reminderKey, a.config.coreConfig.HostAddress, targetActorAddress)
						close(stopChan.(chan struct{}))
						a.activeReminders.Delete(reminderKey)
					}
				}
			}
		}()
	}
	wg.Wait()
	<-a.evaluationChan
}

func (a *actorsRuntime) getRemindersForActorType(ctx context.Context, actorType string, migrate bool) ([]core.ActorReminderReference, *core.ActorMetadata, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, nil, err
	}

	actorMetadata, err := a.actorsReminders.GetActorTypeMetadata(ctx, actorType, migrate)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read actor type metadata: %w", err)
	}

	policyDef := a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore)

	log.Debugf(
		"starting to read reminders for actor type %s (migrate=%t), with metadata id %s and %d partitions",
		actorType, migrate, actorMetadata.ID, actorMetadata.RemindersMetadata.PartitionCount)

	if actorMetadata.RemindersMetadata.PartitionCount >= 1 {
		metadata := map[string]string{metadataPartitionKey: actorMetadata.ID}
		actorMetadata.RemindersMetadata.PartitionsEtag = map[uint32]*string{}

		keyPartitionMap := make(map[string]uint32, actorMetadata.RemindersMetadata.PartitionCount)
		getRequests := make([]state.GetRequest, actorMetadata.RemindersMetadata.PartitionCount)
		for i := uint32(1); i <= uint32(actorMetadata.RemindersMetadata.PartitionCount); i++ {
			key := actorMetadata.CalculateRemindersStateKey(actorType, i)
			keyPartitionMap[key] = i
			getRequests[i-1] = state.GetRequest{
				Key:      key,
				Metadata: metadata,
			}
		}

		var bulkResponse []state.BulkGetResponse
		policyRunner := resiliency.NewRunner[[]state.BulkGetResponse](ctx, policyDef)
		bulkResponse, err = policyRunner(func(ctx context.Context) ([]state.BulkGetResponse, error) {
			return store.BulkGet(ctx, getRequests, state.BulkGetOpts{})
		})
		if err != nil {
			return nil, nil, err
		}

		list := []core.ActorReminderReference{}
		for _, resp := range bulkResponse {
			partition := keyPartitionMap[resp.Key]
			actorMetadata.RemindersMetadata.PartitionsEtag[partition] = resp.ETag
			if resp.Error != "" {
				return nil, nil, fmt.Errorf("could not get reminders partition %v: %v", resp.Key, resp.Error)
			}

			var batch []core.Reminder
			if len(resp.Data) > 0 {
				err = json.Unmarshal(resp.Data, &batch)
				if err != nil {
					return nil, nil, fmt.Errorf("could not parse actor reminders partition %v: %w", resp.Key, err)
				}
			} else {
				return nil, nil, fmt.Errorf("no data found for reminder partition %v: %w", resp.Key, err)
			}

			// We can't pre-allocate "list" with the needed capacity because we don't know how many items are in each partition
			// However, we can limit the number of times we call "append" on list in a way that could cause the slice to be re-allocated, by managing a separate list here with a fixed capacity and modify "list" just once at per iteration on "bulkResponse".
			batchList := make([]core.ActorReminderReference, len(batch))
			for j := range batch {
				batchList[j] = core.ActorReminderReference{
					ActorMetadataID:           actorMetadata.ID,
					ActorRemindersPartitionID: partition,
					Reminder:                  batch[j],
				}
			}
			list = append(list, batchList...)
		}

		log.Debugf(
			"finished reading reminders for actor type %s (migrate=%t), with metadata id %s and %d partitions: total of %d reminders",
			actorType, migrate, actorMetadata.ID, actorMetadata.RemindersMetadata.PartitionCount, len(list))
		return list, actorMetadata, nil
	}

	key := constructCompositeKey("actors", actorType)
	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx, policyDef)
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, &state.GetRequest{
			Key: key,
		})
	})
	if err != nil {
		return nil, nil, err
	}

	if resp == nil {
		resp = &state.GetResponse{}
	}
	log.Debugf("read reminders from %s without partition", key)

	var reminders []core.Reminder
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &reminders)
		if err != nil {
			return nil, nil, fmt.Errorf("could not parse actor reminders: %w", err)
		}
	}

	reminderRefs := make([]core.ActorReminderReference, len(reminders))
	for j := range reminders {
		reminderRefs[j] = core.ActorReminderReference{
			ActorMetadataID:           actorMetadata.ID,
			ActorRemindersPartitionID: 0,
			Reminder:                  reminders[j],
		}
	}

	actorMetadata.RemindersMetadata.PartitionsEtag = map[uint32]*string{
		0: resp.ETag,
	}

	log.Debugf(
		"finished reading reminders for actor type %s (migrate=%t), with metadata id %s and no partitions: total of %d reminders",
		actorType, migrate, actorMetadata.ID, len(reminderRefs))
	return reminderRefs, actorMetadata, nil
}

func newActor(actorType, actorID string, maxReentrancyDepth *int, cl clock.Clock) *core.Actor {
	if cl == nil {
		cl = &clock.RealClock{}
	}
	return &core.Actor{
		ActorType:    actorType,
		ActorID:      actorID,
		ActorLock:    core.NewActorLock(int32(*maxReentrancyDepth)),
		Clock:        cl,
		LastUsedTime: cl.Now().UTC(),
	}
}

func (a *actorsRuntime) RegisterInternalActor(ctx context.Context, actorType string, actor core.InternalActor) error {
	if !a.haveCompatibleStorage() {
		return fmt.Errorf("unable to register internal actor '%s': %w", actorType, ErrIncompatibleStateStore)
	}

	if _, exists := a.internalActors[actorType]; exists {
		return fmt.Errorf("actor type %s already registered", actorType)
	} else {
		if err := a.internalActorChannel.AddInternalActor(actorType, actor); err != nil {
			return err
		}
		a.internalActors[actorType] = actor

		log.Debugf("registering internal actor type: %s", actorType)
		actor.SetActorRuntime(a)
		a.config.coreConfig.HostedActorTypes = append(a.config.coreConfig.HostedActorTypes, actorType)
		if a.placement != nil {
			if err := a.placement.AddHostedActorType(actorType); err != nil {
				return fmt.Errorf("error updating hosted actor types: %s", err)
			}
		}
	}
	return nil
}

func (a *actorsRuntime) GetActorsReminders() core.Reminders {
	return a.actorsReminders
}

func (a *actorsRuntime) GetActorsTimers() core.Timers {
	return a.actorsTimers
}

func (a *actorsRuntime) GetActiveActorsCount(ctx context.Context) []*runtimev1pb.ActiveActorsCount {
	actorCountMap := make(map[string]int32, len(a.config.coreConfig.HostedActorTypes))
	for _, actorType := range a.config.coreConfig.HostedActorTypes {
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
	return strings.HasPrefix(actorType, core.InternalActorTypePrefix)
}

// Stop closes all network connections and resources used in actor runtime.
func (a *actorsRuntime) Stop() {
	if a.placement != nil {
		a.placement.Stop()
	}
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
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

func (a *actorsRuntime) stateStore() (core.TransactionalStateStore, error) {
	storeS, ok := a.compStore.GetStateStore(a.storeName)
	if !ok {
		return nil, errors.New(errStateStoreNotFound)
	}

	store, ok := storeS.(core.TransactionalStateStore)
	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		return nil, errors.New(errStateStoreNotConfigured)
	}

	return store, nil
}
