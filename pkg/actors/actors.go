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
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/actors/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/timers"
	"github.com/dapr/dapr/pkg/channel"
	configuration "github.com/dapr/dapr/pkg/config"
	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
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

	errStateStoreNotFound      = "actors: state store does not exist or incorrectly configured"
	errStateStoreNotConfigured = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

var (
	log = logger.NewLogger("dapr.runtime.actor")

	ErrIncompatibleStateStore   = errors.New("actor state store does not exist, or does not support transactions which are required to save state - please see https://docs.dapr.io/operations/components/setup-state-store/supported-state-stores/")
	ErrDaprResponseHeader       = errors.New("error indicated via actor header response")
	ErrReminderOpActorNotHosted = errors.New("operations on actor reminders are only possible on hosted actor types")
	ErrReminderCanceled         = internal.ErrReminderCanceled
)

// Actors allow calling into virtual actors as well as actor state management.
//
//nolint:interfacebloat
type Actors interface {
	Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
	Init() error
	Stop()
	GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error)
	TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) error
	GetReminder(ctx context.Context, req *GetReminderRequest) (*internal.Reminder, error)
	CreateReminder(ctx context.Context, req *CreateReminderRequest) error
	DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error
	RenameReminder(ctx context.Context, req *RenameReminderRequest) error
	CreateTimer(ctx context.Context, req *CreateTimerRequest) error
	DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error
	IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool
	GetActiveActorsCount(ctx context.Context) []*runtimev1pb.ActiveActorsCount
	RegisterInternalActor(ctx context.Context, actorType string, actor InternalActor) error
}

// GRPCConnectionFn is the type of the function that returns a gRPC connection
type GRPCConnectionFn func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)

type actorsRuntime struct {
	appChannel           channel.AppChannel
	placement            internal.PlacementService
	grpcConnectionFn     GRPCConnectionFn
	actorsConfig         Config
	timers               internal.TimersProvider
	actorsReminders      internal.RemindersProvider
	actorsTable          *sync.Map
	appHealthy           *atomic.Bool
	certChain            *daprCredentials.CertChain
	tracingSpec          configuration.TracingSpec
	resiliency           resiliency.Provider
	storeName            string
	compStore            *compstore.ComponentStore
	ctx                  context.Context
	cancel               context.CancelFunc
	clock                clock.WithTicker
	internalActors       map[string]InternalActor
	internalActorChannel *internalActorChannel

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool
}

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
	MockPlacement internal.PlacementService
}

// NewActors create a new actors runtime with given config.
func NewActors(opts ActorsOpts) Actors {
	return newActorsWithClock(opts, &clock.RealClock{})
}

func newActorsWithClock(opts ActorsOpts, clock clock.WithTicker) Actors {
	appHealthy := &atomic.Bool{}
	appHealthy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())

	remindersProvider := reminders.NewRemindersProvider(clock, internal.RemindersProviderOpts{
		StoreName: opts.StateStoreName,
		Config:    opts.Config.Config,
	})

	a := &actorsRuntime{
		appChannel:           opts.AppChannel,
		grpcConnectionFn:     opts.GRPCConnectionFn,
		actorsConfig:         opts.Config,
		timers:               timers.NewTimersProvider(clock),
		actorsReminders:      remindersProvider,
		certChain:            opts.CertChain,
		tracingSpec:          opts.TracingSpec,
		resiliency:           opts.Resiliency,
		storeName:            opts.StateStoreName,
		placement:            opts.MockPlacement,
		actorsTable:          &sync.Map{},
		appHealthy:           appHealthy,
		ctx:                  ctx,
		cancel:               cancel,
		clock:                clock,
		internalActors:       map[string]InternalActor{},
		internalActorChannel: newInternalActorChannel(),
		compStore:            opts.CompStore,

		// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
		stateTTLEnabled: opts.StateTTLEnabled,
	}

	a.timers.SetExecuteTimerFn(a.executeTimer)
	a.actorsReminders.SetExecuteReminderFn(a.executeReminder)
	a.actorsReminders.SetResiliencyProvider(a.resiliency)
	a.actorsReminders.SetStateStoreProviderFn(a.stateStore)
	a.actorsReminders.SetLookupActorFn(a.isActorLocallyHosted)

	return a
}

func (a *actorsRuntime) isActorLocallyHosted(actorType string, actorID string) (isLocal bool, actorAddress string) {
	targetActorAddress, _ := a.placement.LookupActor(actorType, actorID)
	if targetActorAddress == "" {
		log.Warn("Did not find address for actor with actorType %s and actorID %s", actorType, actorID)
		return false, ""
	}

	if a.isActorLocal(targetActorAddress, a.actorsConfig.Config.HostAddress, a.actorsConfig.Config.Port) {
		return true, targetActorAddress
	}
	return false, targetActorAddress
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
	conf := a.actorsConfig.Config
	if len(conf.PlacementAddresses) == 0 {
		return errors.New("actors: couldn't connect to placement service: address is empty")
	}

	if len(a.actorsConfig.Config.HostedActorTypes) > 0 {
		if !a.haveCompatibleStorage() {
			return ErrIncompatibleStateStore
		}
	}

	hostname := net.JoinHostPort(a.actorsConfig.Config.HostAddress, strconv.Itoa(a.actorsConfig.Config.Port))

	a.actorsReminders.Init(context.TODO())
	a.timers.Init(context.TODO())

	if a.placement == nil {
		a.placement = placement.NewActorPlacement(placement.ActorPlacementOpts{
			ServerAddrs:     a.actorsConfig.Config.PlacementAddresses,
			CertChain:       a.certChain,
			AppID:           a.actorsConfig.Config.AppID,
			RuntimeHostname: hostname,
			PodName:         a.actorsConfig.Config.PodName,
			ActorTypes:      a.actorsConfig.Config.HostedActorTypes.ListActorTypes(),
			AppHealthFn: func() bool {
				return a.appHealthy.Load()
			},
			AfterTableUpdateFn: func() {
				a.drainRebalancedActors()
				a.actorsReminders.OnPlacementTablesUpdated(context.TODO())
			},
		})
	}

	go a.placement.Start(context.TODO())
	go a.deactivationTicker(a.actorsConfig, a.deactivateActor)

	log.Infof("Actor runtime started. Actor idle timeout: %v. Actor scan interval: %v",
		a.actorsConfig.Config.ActorIdleTimeout, a.actorsConfig.Config.ActorDeactivationScanInterval)

	// Be careful to configure healthz endpoint option. If app healthz returns unhealthy status, Dapr will
	// disconnect from placement to remove the node from consistent hashing ring.
	// i.e if app is busy state, the healthz status would be flaky, which leads to frequent
	// actor rebalancing. It will impact the entire service.
	go a.startAppHealthCheck(
		health.WithFailureThreshold(4),
		health.WithInterval(5*time.Second),
		health.WithRequestTimeout(2*time.Second),
		health.WithHTTPClient(a.actorsConfig.Config.HealthHTTPClient),
	)

	return nil
}

func (a *actorsRuntime) startAppHealthCheck(opts ...health.Option) {
	if len(a.actorsConfig.Config.HostedActorTypes) == 0 || a.appChannel == nil {
		return
	}

	ch := health.StartEndpointHealthCheck(a.ctx, a.actorsConfig.Config.HealthEndpoint+"/healthz", opts...)
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
		WithHTTPExtension(http.MethodDelete, "").
		WithContentType(invokev1.JSONContentType)
	defer req.Close()

	// TODO Propagate context.
	ctx := context.TODO()

	resp, err := a.getAppChannel(actorType).InvokeMethod(ctx, req, "")
	if err != nil {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "invoke")
		return err
	}
	defer resp.Close()

	if resp.Status().Code != http.StatusOK {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "status_code_"+strconv.FormatInt(int64(resp.Status().Code), 10))
		body, _ := resp.RawDataFull()
		return fmt.Errorf("error from actor service: %s", string(body))
	}

	a.removeActorFromTable(actorType, actorID)
	diag.DefaultMonitoring.ActorDeactivated(actorType)
	log.Debugf("Deactivated actor type=%s, id=%s", actorType, actorID)

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
	ticker := a.clock.NewTicker(configuration.ActorDeactivationScanInterval)
	ch := ticker.C()
	defer ticker.Stop()

	for {
		select {
		case t := <-ch:
			a.actorsTable.Range(func(key, value interface{}) bool {
				actorInstance := value.(*actor)

				if actorInstance.isBusy() {
					return true
				}

				durationPassed := t.Sub(actorInstance.lastUsedTime)
				if durationPassed >= configuration.GetIdleTimeoutForType(actorInstance.actorType) {
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
	err := a.placement.WaitUntilReady(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for placement readiness: %w", err)
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
	if a.isActorLocal(lar.targetActorAddress, a.actorsConfig.Config.HostAddress, a.actorsConfig.Config.Port) {
		resp, err = a.callLocalActor(ctx, req)
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

func (a *actorsRuntime) getOrCreateActor(actorType, actorID string) *actor {
	key := constructCompositeKey(actorType, actorID)

	// This avoids allocating multiple actor allocations by calling newActor
	// whenever actor is invoked. When storing actor key first, there is a chance to
	// call newActor, but this is trivial.
	val, ok := a.actorsTable.Load(key)
	if !ok {
		val, _ = a.actorsTable.LoadOrStore(key, newActor(actorType, actorID, a.actorsConfig.GetReentrancyForType(actorType).MaxStackDepth, a.clock))
	}

	return val.(*actor)
}

func (a *actorsRuntime) callLocalActor(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	actorTypeID := req.Actor()

	act := a.getOrCreateActor(actorTypeID.GetActorType(), actorTypeID.GetActorId())

	// Reentrancy to determine how we lock.
	var reentrancyID *string
	if a.actorsConfig.GetReentrancyForType(act.actorType).Enabled {
		if headerValue, ok := req.Metadata()["Dapr-Reentrancy-Id"]; ok {
			reentrancyID = &headerValue.GetValues()[0]
		} else {
			uuidObj, err := uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("failed to generate UUID: %w", err)
			}
			uuid := uuidObj.String()
			req.AddMetadata(map[string][]string{
				"Dapr-Reentrancy-Id": {uuid},
			})
			reentrancyID = &uuid
		}
	}

	err := act.lock(reentrancyID)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	defer act.unlock()

	// Replace method to actors method.
	msg := req.Message()
	originalMethod := msg.Method
	msg.Method = "actors/" + actorTypeID.ActorType + "/" + actorTypeID.ActorId + "/method/" + msg.Method

	// Reset the method so we can perform retries.
	defer func() {
		msg.Method = originalMethod
	}()

	// Original code overrides method with PUT. Why?
	if msg.GetHttpExtension() == nil {
		req.WithHTTPExtension(http.MethodPut, "")
	} else {
		msg.HttpExtension.Verb = commonv1pb.HTTPExtension_PUT //nolint:nosnakecase
	}

	policyDef := a.resiliency.ActorPostLockPolicy(act.actorType, act.actorID)

	// If the request can be retried, we need to enable replaying
	if policyDef != nil && policyDef.HasRetries() {
		req.WithReplay(true)
	}

	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.getAppChannel(act.actorType).InvokeMethod(ctx, req, "")
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, errors.New("error from actor service: response object is nil")
	}

	if resp.Status().Code != http.StatusOK {
		respData, _ := resp.RawDataFull()
		return nil, fmt.Errorf("error from actor service: %s", string(respData))
	}

	// The .NET SDK signifies Actor failure via a header instead of a bad response.
	if _, ok := resp.Headers()["X-Daprerrorresponseheader"]; ok {
		return resp, ErrDaprResponseHeader
	}

	return resp, nil
}

func (a *actorsRuntime) getAppChannel(actorType string) channel.AppChannel {
	if a.internalActorChannel.Contains(actorType) {
		return a.internalActorChannel
	}
	return a.appChannel
}

func (a *actorsRuntime) callRemoteActor(
	ctx context.Context,
	targetAddress, targetID string,
	req *invokev1.InvokeMethodRequest,
) (*invokev1.InvokeMethodResponse, func(destroy bool), error) {
	conn, teardown, err := a.grpcConnectionFn(context.TODO(), targetAddress, targetID, a.actorsConfig.Config.Namespace)
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

func (a *actorsRuntime) GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}

	actorKey := req.ActorKey()
	partitionKey := constructCompositeKey(a.actorsConfig.Config.AppID, actorKey)
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
		return &StateResponse{}, nil
	}

	return &StateResponse{
		Data: resp.Data,
	}, nil
}

func (a *actorsRuntime) TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) error {
	store, err := a.stateStore()
	if err != nil {
		return err
	}

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

	return a.executeStateStoreTransaction(ctx, store, operations, metadata)
}

func (a *actorsRuntime) executeStateStoreTransaction(ctx context.Context, store internal.TransactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error {
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	stateReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.Multi(ctx, stateReq)
	})
	return err
}

func (a *actorsRuntime) IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool {
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
	return constructCompositeKey(a.actorsConfig.Config.AppID, actorKey, key)
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
			if address != "" && !a.isActorLocal(address, a.actorsConfig.Config.HostAddress, a.actorsConfig.Config.Port) {
				// actor has been moved to a different host, deactivate when calls are done cancel any reminders
				// each item in reminders contain a struct with some metadata + the actual reminder struct
				a.actorsReminders.DrainRebalancedReminders(actorType, actorID)

				actor := value.(*actor)
				if a.actorsConfig.GetDrainRebalancedActorsForType(actorType) {
					// wait until actor isn't busy or timeout hits
					if actor.isBusy() {
						select {
						case <-a.clock.After(a.actorsConfig.Config.DrainOngoingCallTimeout):
							break
						case <-actor.channel():
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
					if !actor.isBusy() {
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

// executeTimer implements timers.ExecuteTimerFn.
func (a *actorsRuntime) executeTimer(reminder *internal.Reminder) bool {
	_, exists := a.actorsTable.Load(reminder.ActorKey())
	if !exists {
		log.Errorf("Could not find active timer %s", reminder.Key())
		return false
	}

	err := a.doExecuteReminderOrTimer(reminder, true)
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
	err := a.doExecuteReminderOrTimer(reminder, false)
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

// Executes a reminder or timer
func (a *actorsRuntime) doExecuteReminderOrTimer(reminder *internal.Reminder, isTimer bool) (err error) {
	var (
		data         any
		logName      string
		invokeMethod string
	)

	if isTimer {
		logName = "timer"
		invokeMethod = "timer/" + reminder.Name
		data = &TimerResponse{
			Callback: reminder.Callback,
			Data:     reminder.Data,
			DueTime:  reminder.DueTime,
			Period:   reminder.Period.String(),
		}
	} else {
		logName = "reminder"
		invokeMethod = "remind/" + reminder.Name
		data = &ReminderResponse{
			DueTime: reminder.DueTime,
			Period:  reminder.Period.String(),
			Data:    reminder.Data,
		}
	}
	policyDef := a.resiliency.ActorPreLockPolicy(reminder.ActorType, reminder.ActorID)

	log.Debug("Executing " + logName + " for actor " + reminder.Key())
	req := invokev1.NewInvokeMethodRequest(invokeMethod).
		WithActor(reminder.ActorType, reminder.ActorID).
		WithDataObject(data).
		WithContentType(invokev1.JSONContentType)
	if policyDef != nil {
		req.WithReplay(policyDef.HasRetries())
	}
	defer req.Close()

	policyRunner := resiliency.NewRunnerWithOptions(context.TODO(), policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	imr, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.callLocalActor(ctx, req)
	})
	if err != nil && !errors.Is(err, internal.ErrReminderCanceled) {
		log.Errorf("Error executing %s for actor %s: %v", logName, reminder.Key(), err)
	}
	if imr != nil {
		_ = imr.Close()
	}
	return err
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

// Deprecated: Currently RenameReminder renames by deleting-then-inserting-again.
// This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder
func (a *actorsRuntime) RenameReminder(ctx context.Context, req *RenameReminderRequest) error {
	if !a.actorsConfig.Config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	return a.actorsReminders.RenameReminder(ctx, req)
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

func (a *actorsRuntime) RegisterInternalActor(ctx context.Context, actorType string, actor InternalActor) error {
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

		log.Debugf("Registering internal actor type: %s", actorType)
		actor.SetActorRuntime(a)
		a.actorsConfig.Config.HostedActorTypes.AddActorType(actorType)
		if a.placement != nil {
			if err := a.placement.AddHostedActorType(actorType); err != nil {
				return fmt.Errorf("error updating hosted actor types: %s", err)
			}
		}
	}
	return nil
}

func (a *actorsRuntime) GetActiveActorsCount(ctx context.Context) []*runtimev1pb.ActiveActorsCount {
	actorCountMap := make(map[string]int32, len(a.actorsConfig.Config.HostedActorTypes))
	for actorType := range a.actorsConfig.Config.HostedActorTypes {
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
func (a *actorsRuntime) Stop() {
	if a.placement != nil {
		err := a.placement.Close()
		if err != nil {
			log.Warnf("Failed to close placement service: %v", err)
		}
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

func (a *actorsRuntime) stateStore() (internal.TransactionalStateStore, error) {
	storeS, ok := a.compStore.GetStateStore(a.storeName)
	if !ok {
		return nil, errors.New(errStateStoreNotFound)
	}

	store, ok := storeS.(internal.TransactionalStateStore)
	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		return nil, errors.New(errStateStoreNotConfigured)
	}

	return store, nil
}
