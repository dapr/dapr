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
	"hash/fnv"
	"net"
	nethttp "net/http"
	"reflect"
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
	"github.com/dapr/dapr/pkg/actors/reminders"
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
	metadataZeroID       = "00000000-0000-0000-0000-000000000000"

	errStateStoreNotFound      = "actors: state store does not exist or incorrectly configured"
	errStateStoreNotConfigured = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

var log = logger.NewLogger("dapr.runtime.actor")

// Actors allow calling into virtual actors as well as actor state management.
//
//nolint:interfacebloat
type Actors interface {
	Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
	Init() error
	Stop()
	GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error)
	TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) error
	GetReminder(ctx context.Context, req *GetReminderRequest) (*reminders.Reminder, error)
	CreateReminder(ctx context.Context, req *CreateReminderRequest) error
	DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error
	RenameReminder(ctx context.Context, req *RenameReminderRequest) error
	CreateTimer(ctx context.Context, req *CreateTimerRequest) error
	DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error
	IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool
	GetActiveActorsCount(ctx context.Context) []*runtimev1pb.ActiveActorsCount
	RegisterInternalActor(ctx context.Context, actorType string, actor InternalActor) error
}

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

type transactionalStateStore interface {
	state.Store
	state.TransactionalStore
}

type actorsRuntime struct {
	appChannel            channel.AppChannel
	placement             PlacementService
	grpcConnectionFn      GRPCConnectionFn
	config                Config
	actorsTable           *sync.Map
	activeTimers          *sync.Map
	activeTimersCount     map[string]*int64
	activeTimersCountLock sync.RWMutex
	activeTimersLock      sync.RWMutex
	activeReminders       *sync.Map
	remindersLock         sync.RWMutex
	remindersStoringLock  sync.Mutex
	reminders             map[string][]actorReminderReference
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
	internalActors        map[string]InternalActor
	internalActorChannel  *internalActorChannel

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool
}

// ActorMetadata represents information about the actor type.
type ActorMetadata struct {
	ID                string                 `json:"id"`
	RemindersMetadata ActorRemindersMetadata `json:"actorRemindersMetadata"`
	Etag              *string                `json:"-"`
}

// ActorRemindersMetadata represents information about actor's reminders.
type ActorRemindersMetadata struct {
	PartitionCount int                `json:"partitionCount"`
	partitionsEtag map[uint32]*string `json:"-"`
}

type actorReminderReference struct {
	actorMetadataID           string
	actorRemindersPartitionID uint32
	reminder                  reminders.Reminder
}

var (
	ErrIncompatibleStateStore   = errors.New("actor state store does not exist, or does not support transactions which are required to save state - please see https://docs.dapr.io/operations/components/setup-state-store/supported-state-stores/")
	ErrDaprResponseHeader       = errors.New("error indicated via actor header response")
	ErrReminderCanceled         = errors.New("reminder has been canceled")
	ErrReminderOpActorNotHosted = errors.New("operations on actor reminders are only possible on hosted actor types")
)

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
func NewActors(opts ActorsOpts) Actors {
	return newActorsWithClock(opts, &clock.RealClock{})
}

func newActorsWithClock(opts ActorsOpts, clock clock.WithTicker) Actors {
	appHealthy := &atomic.Bool{}
	appHealthy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	return &actorsRuntime{
		appChannel:           opts.AppChannel,
		grpcConnectionFn:     opts.GRPCConnectionFn,
		config:               opts.Config,
		certChain:            opts.CertChain,
		tracingSpec:          opts.TracingSpec,
		resiliency:           opts.Resiliency,
		storeName:            opts.StateStoreName,
		placement:            opts.MockPlacement,
		actorsTable:          &sync.Map{},
		activeTimers:         &sync.Map{},
		activeTimersCount:    make(map[string]*int64),
		activeReminders:      &sync.Map{},
		reminders:            map[string][]actorReminderReference{},
		evaluationChan:       make(chan struct{}, 1),
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
	if len(a.config.PlacementAddresses) == 0 {
		return errors.New("actors: couldn't connect to placement service: address is empty")
	}

	if len(a.config.HostedActorTypes) > 0 {
		if !a.haveCompatibleStorage() {
			return ErrIncompatibleStateStore
		}
	}

	hostname := net.JoinHostPort(a.config.HostAddress, strconv.Itoa(a.config.Port))

	afterTableUpdateFn := func() {
		a.drainRebalancedActors()
		a.evaluateReminders(context.TODO())
	}
	appHealthFn := func() bool { return a.appHealthy.Load() }

	if a.placement == nil {
		a.placement = internal.NewActorPlacement(
			a.config.PlacementAddresses, a.certChain,
			a.config.AppID, hostname, a.config.PodName,
			a.config.HostedActorTypes.ListActorTypes(),
			appHealthFn,
			afterTableUpdateFn,
		)
	}

	go a.placement.Start()
	go a.deactivationTicker(a.config, a.deactivateActor)

	log.Infof("actor runtime started. actor idle timeout: %v. actor scan interval: %v",
		a.config.ActorIdleTimeout, a.config.ActorDeactivationScanInterval)

	// Be careful to configure healthz endpoint option. If app healthz returns unhealthy status, Dapr will
	// disconnect from placement to remove the node from consistent hashing ring.
	// i.e if app is busy state, the healthz status would be flaky, which leads to frequent
	// actor rebalancing. It will impact the entire service.
	go a.startAppHealthCheck(
		health.WithFailureThreshold(4),
		health.WithInterval(5*time.Second),
		health.WithRequestTimeout(2*time.Second),
		health.WithHTTPClient(a.config.HealthHTTPClient),
	)

	return nil
}

func (a *actorsRuntime) startAppHealthCheck(opts ...health.Option) {
	if len(a.config.HostedActorTypes) == 0 || a.appChannel == nil {
		return
	}

	ch := health.StartEndpointHealthCheck(a.ctx, a.config.HealthEndpoint+"/healthz", opts...)
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

	resp, err := a.getAppChannel(actorType).InvokeMethod(ctx, req, "")
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
	if a.isActorLocal(lar.targetActorAddress, a.config.HostAddress, a.config.Port) {
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
		val, _ = a.actorsTable.LoadOrStore(key, newActor(actorType, actorID, a.config.GetReentrancyForType(actorType).MaxStackDepth, a.clock))
	}

	return val.(*actor)
}

func (a *actorsRuntime) callLocalActor(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	actorTypeID := req.Actor()

	act := a.getOrCreateActor(actorTypeID.GetActorType(), actorTypeID.GetActorId())

	// Reentrancy to determine how we lock.
	var reentrancyID *string
	if a.config.GetReentrancyForType(act.actorType).Enabled {
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
		req.WithHTTPExtension(nethttp.MethodPut, "")
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

	if resp.Status().Code != nethttp.StatusOK {
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
	conn, teardown, err := a.grpcConnectionFn(context.TODO(), targetAddress, targetID, a.config.Namespace)
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
	partitionKey := constructCompositeKey(a.config.AppID, actorKey)
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
	baseKey := constructCompositeKey(a.config.AppID, req.ActorKey())
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

func (a *actorsRuntime) executeStateStoreTransaction(ctx context.Context, store transactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error {
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
	return constructCompositeKey(a.config.AppID, actorKey, key)
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
			if address != "" && !a.isActorLocal(address, a.config.HostAddress, a.config.Port) {
				// actor has been moved to a different host, deactivate when calls are done cancel any reminders
				// each item in reminders contain a struct with some metadata + the actual reminder struct
				a.remindersLock.RLock()
				reminders := a.reminders[actorType]
				a.remindersLock.RUnlock()
				for _, r := range reminders {
					// r.reminder refers to the actual reminder struct that is saved in the db
					if r.reminder.ActorType == actorType && r.reminder.ActorID == actorID {
						reminderKey := constructCompositeKey(actorKey, r.reminder.Name)
						stopChan, exists := a.activeReminders.Load(reminderKey)
						if exists {
							close(stopChan.(chan struct{}))
							a.activeReminders.Delete(reminderKey)
						}
					}
				}

				actor := value.(*actor)
				if a.config.GetDrainRebalancedActorsForType(actorType) {
					// wait until actor isn't busy or timeout hits
					if actor.isBusy() {
						select {
						case <-a.clock.After(a.config.DrainOngoingCallTimeout):
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

func (a *actorsRuntime) evaluateReminders(ctx context.Context) {
	a.evaluationLock.Lock()
	defer a.evaluationLock.Unlock()

	a.evaluationChan <- struct{}{}

	var wg sync.WaitGroup
	for t := range a.config.HostedActorTypes {
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
				rmd := vals[i].reminder
				reminderKey := rmd.Key()
				targetActorAddress, _ := a.placement.LookupActor(rmd.ActorType, rmd.ActorID)
				if targetActorAddress == "" {
					log.Warn("Did not find address for actor for reminder " + reminderKey)
					continue
				}

				if a.isActorLocal(targetActorAddress, a.config.HostAddress, a.config.Port) {
					_, exists := a.activeReminders.Load(reminderKey)

					if !exists {
						stop := make(chan struct{})
						a.activeReminders.Store(reminderKey, stop)
						err := a.startReminder(&rmd, stop)
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
						log.Debugf("Stopping reminder %s on %s as it's active on host %s", reminderKey, a.config.HostAddress, targetActorAddress)
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

func (a *actorsRuntime) getReminderTrack(ctx context.Context, key string) (*reminders.ReminderTrack, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}

	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	storeReq := &state.GetRequest{
		Key: key,
	}
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, storeReq)
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		resp = &state.GetResponse{}
	}
	track := &reminders.ReminderTrack{
		RepetitionLeft: -1,
	}
	_ = json.Unmarshal(resp.Data, track)
	track.Etag = resp.ETag
	return track, nil
}

func (a *actorsRuntime) updateReminderTrack(ctx context.Context, key string, repetition int, lastInvokeTime time.Time, etag *string) error {
	store, err := a.stateStore()
	if err != nil {
		return err
	}

	track := reminders.ReminderTrack{
		LastFiredTime:  lastInvokeTime,
		RepetitionLeft: repetition,
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	setReq := &state.SetRequest{
		Key:   key,
		Value: track,
		ETag:  etag,
		Options: state.SetStateOption{
			Concurrency: state.FirstWrite,
		},
	}
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Set(ctx, setReq)
	})
	return err
}

func (a *actorsRuntime) startReminder(reminder *reminders.Reminder, stopChannel chan struct{}) error {
	reminderKey := reminder.Key()

	track, err := a.getReminderTrack(context.TODO(), reminderKey)
	if err != nil {
		return fmt.Errorf("error getting reminder track: %w", err)
	}

	reminder.UpdateFromTrack(track)

	go func() {
		var (
			ttlTimer, nextTimer clock.Timer
			ttlTimerC           <-chan time.Time
			err                 error
		)
		eTag := track.Etag

		if !reminder.ExpirationTime.IsZero() {
			ttlTimer = a.clock.NewTimer(reminder.ExpirationTime.Sub(a.clock.Now()))
			ttlTimerC = ttlTimer.C()
		}

		nextTimer = a.clock.NewTimer(reminder.NextTick().Sub(a.clock.Now()))
		defer func() {
			if nextTimer != nil && !nextTimer.Stop() {
				<-nextTimer.C()
			}
			if ttlTimer != nil && !ttlTimer.Stop() {
				<-ttlTimer.C()
			}
		}()

	L:
		for {
			select {
			case <-nextTimer.C():
				// noop
			case <-ttlTimerC:
				// proceed with reminder deletion
				log.Infof("Reminder %s with parameters: dueTime: %s, period: %s has expired", reminderKey, reminder.DueTime, reminder.Period)
				ttlTimer = nil
				break L
			case <-stopChannel:
				// reminder has been already deleted
				log.Infof("Reminder %s with parameters: dueTime: %s, period: %s has been deleted", reminderKey, reminder.DueTime, reminder.Period)
				return
			}

			_, exists := a.activeReminders.Load(reminderKey)
			if !exists {
				log.Error("Could not find active reminder with key: " + reminderKey)
				nextTimer = nil
				return
			}

			// If all repetitions are completed, delete the reminder and do not execute it
			if reminder.RepeatsLeft() == 0 {
				log.Info("Reminder " + reminderKey + " has been completed")
				nextTimer = nil
				break L
			}

			err = a.executeReminder(reminder, false)
			diag.DefaultMonitoring.ActorReminderFired(reminder.ActorType, err == nil)
			if err != nil {
				if errors.Is(err, ErrReminderCanceled) {
					// The handler is explicitly canceling the timer
					log.Debug("Reminder " + reminderKey + " was canceled by the actor")
					nextTimer = nil
					break L
				} else {
					log.Errorf("Error while executing reminder %s: %v", reminderKey, err)
				}
			}

			_, exists = a.activeReminders.Load(reminderKey)
			if exists {
				err = a.updateReminderTrack(context.TODO(), reminderKey, reminder.RepeatsLeft(), reminder.NextTick(), eTag)
				if err != nil {
					log.Errorf("Error updating reminder track for reminder %s: %v", reminderKey, err)
				}
				track, gErr := a.getReminderTrack(context.TODO(), reminderKey)
				if gErr != nil {
					log.Errorf("Error retrieving reminder %s: %v", reminderKey, gErr)
				} else {
					eTag = track.Etag
				}
			} else {
				log.Error("Could not find active reminder with key: " + reminderKey)
				nextTimer = nil
				return
			}

			if reminder.TickExecuted() {
				nextTimer = nil
				break L
			}

			nextTimer.Reset(reminder.NextTick().Sub(a.clock.Now()))
		}

		err = a.DeleteReminder(context.TODO(), &DeleteReminderRequest{
			Name:      reminder.Name,
			ActorID:   reminder.ActorID,
			ActorType: reminder.ActorType,
		})
		if err != nil {
			log.Errorf("error deleting reminder: %s", err)
		}
	}()

	return nil
}

// Executes a reminder or timer
func (a *actorsRuntime) executeReminder(reminder *reminders.Reminder, isTimer bool) (err error) {
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
	if err != nil && !errors.Is(err, ErrReminderCanceled) {
		log.Errorf("Error executing %s for actor %s: %v", logName, reminder.Key(), err)
	}
	if imr != nil {
		_ = imr.Close()
	}
	return err
}

func (a *actorsRuntime) reminderRequiresUpdate(new *reminders.Reminder, existing *reminders.Reminder) bool {
	// If the reminder is different, short-circuit
	if existing.ActorID != new.ActorID ||
		existing.ActorType != new.ActorType ||
		existing.Name != new.Name {
		return false
	}

	return existing.DueTime != new.DueTime ||
		existing.Period != new.Period ||
		!new.ExpirationTime.IsZero() ||
		(!existing.ExpirationTime.IsZero() && new.ExpirationTime.IsZero()) ||
		!reflect.DeepEqual(existing.Data, new.Data)
}

func (a *actorsRuntime) getReminder(reminderName string, actorType string, actorID string) (*reminders.Reminder, bool) {
	a.remindersLock.RLock()
	reminders := a.reminders[actorType]
	a.remindersLock.RUnlock()

	for _, r := range reminders {
		if r.reminder.ActorID == actorID && r.reminder.ActorType == actorType && r.reminder.Name == reminderName {
			return &r.reminder, true
		}
	}

	return nil, false
}

func (m *ActorMetadata) calculateReminderPartition(actorID, reminderName string) uint32 {
	if m.RemindersMetadata.PartitionCount <= 0 {
		return 0
	}

	// do not change this hash function because it would be a breaking change.
	h := fnv.New32a()
	h.Write([]byte(actorID))
	h.Write([]byte(reminderName))
	return (h.Sum32() % uint32(m.RemindersMetadata.PartitionCount)) + 1
}

func (m *ActorMetadata) createReminderReference(reminder reminders.Reminder) actorReminderReference {
	if m.RemindersMetadata.PartitionCount > 0 {
		return actorReminderReference{
			actorMetadataID:           m.ID,
			actorRemindersPartitionID: m.calculateReminderPartition(reminder.ActorID, reminder.Name),
			reminder:                  reminder,
		}
	}

	return actorReminderReference{
		actorMetadataID:           metadataZeroID,
		actorRemindersPartitionID: 0,
		reminder:                  reminder,
	}
}

func (m *ActorMetadata) calculateRemindersStateKey(actorType string, remindersPartitionID uint32) string {
	if remindersPartitionID == 0 {
		return constructCompositeKey("actors", actorType)
	}

	return constructCompositeKey(
		"actors",
		actorType,
		m.ID,
		"reminders",
		strconv.Itoa(int(remindersPartitionID)))
}

func (m *ActorMetadata) calculateEtag(partitionID uint32) *string {
	return m.RemindersMetadata.partitionsEtag[partitionID]
}

func (m *ActorMetadata) removeReminderFromPartition(reminderRefs []actorReminderReference, actorType, actorID, reminderName string) (bool, []reminders.Reminder, string, *string) {
	// First, we find the partition
	var partitionID uint32
	l := len(reminderRefs)
	if m.RemindersMetadata.PartitionCount > 0 {
		var found bool
		for _, reminderRef := range reminderRefs {
			if reminderRef.reminder.ActorType == actorType && reminderRef.reminder.ActorID == actorID && reminderRef.reminder.Name == reminderName {
				partitionID = reminderRef.actorRemindersPartitionID
				found = true
				break
			}
		}

		// If the reminder doesn't exist, return without making any change
		if !found {
			return false, nil, "", nil
		}

		// When calculating the initial allocated size of remindersInPartitionAfterRemoval, if we have partitions assume len(reminderRefs)/PartitionCount for an initial count
		// This is unlikely to avoid all re-allocations, but it's still better than allocating the slice with capacity 0
		l /= m.RemindersMetadata.PartitionCount
	}

	remindersInPartitionAfterRemoval := make([]reminders.Reminder, 0, l)
	var found bool
	for _, reminderRef := range reminderRefs {
		if reminderRef.reminder.ActorType == actorType && reminderRef.reminder.ActorID == actorID && reminderRef.reminder.Name == reminderName {
			found = true
			continue
		}

		// Only the items in the partition to be updated.
		if reminderRef.actorRemindersPartitionID == partitionID {
			remindersInPartitionAfterRemoval = append(remindersInPartitionAfterRemoval, reminderRef.reminder)
		}
	}

	// If no reminder found, return false here to short-circuit the next operations
	if !found {
		return false, nil, "", nil
	}

	stateKey := m.calculateRemindersStateKey(actorType, partitionID)
	return true, remindersInPartitionAfterRemoval, stateKey, m.calculateEtag(partitionID)
}

func (m *ActorMetadata) insertReminderInPartition(reminderRefs []actorReminderReference, reminder reminders.Reminder) ([]reminders.Reminder, actorReminderReference, string, *string) {
	newReminderRef := m.createReminderReference(reminder)

	var remindersInPartitionAfterInsertion []reminders.Reminder
	for _, reminderRef := range reminderRefs {
		// Only the items in the partition to be updated.
		if reminderRef.actorRemindersPartitionID == newReminderRef.actorRemindersPartitionID {
			remindersInPartitionAfterInsertion = append(remindersInPartitionAfterInsertion, reminderRef.reminder)
		}
	}

	remindersInPartitionAfterInsertion = append(remindersInPartitionAfterInsertion, reminder)

	stateKey := m.calculateRemindersStateKey(newReminderRef.reminder.ActorType, newReminderRef.actorRemindersPartitionID)
	return remindersInPartitionAfterInsertion, newReminderRef, stateKey, m.calculateEtag(newReminderRef.actorRemindersPartitionID)
}

func (m *ActorMetadata) calculateDatabasePartitionKey(stateKey string) string {
	if m.RemindersMetadata.PartitionCount > 0 {
		return m.ID
	}

	return stateKey
}

func (a *actorsRuntime) waitForEvaluationChan() bool {
	t := a.clock.NewTimer(5 * time.Second)
	defer t.Stop()
	select {
	case <-a.ctx.Done():
		return false
	case <-t.C():
		return false
	case a.evaluationChan <- struct{}{}:
		<-a.evaluationChan
	}
	return true
}

func (a *actorsRuntime) CreateReminder(ctx context.Context, req *CreateReminderRequest) error {
	if !a.config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	store, err := a.stateStore()
	if err != nil {
		return err
	}

	// Create the new reminder object
	reminder, err := reminders.NewReminderFromCreateReminderRequest(req, a.clock.Now())
	if err != nil {
		return err
	}

	if !a.waitForEvaluationChan() {
		return errors.New("error creating reminder: timed out after 5s")
	}

	a.remindersStoringLock.Lock()
	defer a.remindersStoringLock.Unlock()

	existing, ok := a.getReminder(req.Name, req.ActorType, req.ActorID)
	if ok {
		if a.reminderRequiresUpdate(reminder, existing) {
			err = a.doDeleteReminder(ctx, req.ActorType, req.ActorID, req.Name)
			if err != nil {
				return err
			}
		} else {
			return nil
		}
	}

	stop := make(chan struct{})

	err = a.storeReminder(ctx, store, reminder, stop)
	if err != nil {
		return fmt.Errorf("error storing reminder: %w", err)
	}
	return a.startReminder(reminder, stop)
}

func (a *actorsRuntime) CreateTimer(ctx context.Context, req *CreateTimerRequest) error {
	reminder, err := reminders.NewReminderFromCreateTimerRequest(req, a.clock.Now())
	if err != nil {
		return err
	}

	a.activeTimersLock.Lock()
	defer a.activeTimersLock.Unlock()

	actorKey := reminder.ActorKey()
	timerKey := reminder.Key()

	_, exists := a.actorsTable.Load(actorKey)
	if !exists {
		return fmt.Errorf("can't create timer for actor %s: actor not activated", actorKey)
	}

	stopChan, exists := a.activeTimers.Load(timerKey)
	if exists {
		close(stopChan.(chan struct{}))
	}

	log.Debugf("Create timer '%s' dueTime:'%s' period:'%s' ttl:'%v'",
		timerKey, reminder.DueTime, reminder.Period, reminder.ExpirationTime)

	stop := make(chan struct{}, 1)
	a.activeTimers.Store(timerKey, stop)
	a.updateActiveTimersCount(req.ActorType, 1)

	go func() {
		var (
			ttlTimer, nextTimer clock.Timer
			ttlTimerC           <-chan time.Time
			err                 error
		)

		if !reminder.ExpirationTime.IsZero() {
			ttlTimer = a.clock.NewTimer(reminder.ExpirationTime.Sub(a.clock.Now()))
			ttlTimerC = ttlTimer.C()
		}

		nextTimer = a.clock.NewTimer(reminder.NextTick().Sub(a.clock.Now()))
		defer func() {
			if nextTimer != nil && !nextTimer.Stop() {
				<-nextTimer.C()
			}
			if ttlTimer != nil && !ttlTimer.Stop() {
				<-ttlTimer.C()
			}
		}()

	L:
		for {
			select {
			case <-nextTimer.C():
				// noop
			case <-ttlTimerC:
				// timer has expired; proceed with deletion
				log.Infof("Timer %s with parameters: dueTime: %s, period: %s, TTL: %s has expired", timerKey, req.DueTime, req.Period, req.TTL)
				ttlTimer = nil
				break L
			case <-stop:
				// timer has been already deleted
				log.Infof("Timer %s with parameters: dueTime: %s, period: %s, TTL: %s has been deleted", timerKey, req.DueTime, req.Period, req.TTL)
				return
			}

			if _, exists := a.actorsTable.Load(actorKey); exists {
				err = a.executeReminder(reminder, true)
				diag.DefaultMonitoring.ActorTimerFired(req.ActorType, err == nil)
				if err != nil {
					log.Errorf("error invoking timer on actor %s: %s", actorKey, err)
				}
			} else {
				log.Errorf("Could not find active timer %s", timerKey)
				nextTimer = nil
				return
			}

			if reminder.TickExecuted() {
				log.Infof("Timer %s has been completed", timerKey)
				nextTimer = nil
				break L
			}

			nextTimer.Reset(reminder.NextTick().Sub(a.clock.Now()))
		}

		err = a.DeleteTimer(ctx, &DeleteTimerRequest{
			Name:      req.Name,
			ActorID:   req.ActorID,
			ActorType: req.ActorType,
		})
		if err != nil {
			log.Errorf("error deleting timer %s: %v", timerKey, err)
		}
	}()
	return nil
}

func (a *actorsRuntime) updateActiveTimersCount(actorType string, inc int64) {
	a.activeTimersCountLock.RLock()
	_, ok := a.activeTimersCount[actorType]
	a.activeTimersCountLock.RUnlock()
	if !ok {
		a.activeTimersCountLock.Lock()
		if _, ok = a.activeTimersCount[actorType]; !ok { // re-check
			a.activeTimersCount[actorType] = new(int64)
		}
		a.activeTimersCountLock.Unlock()
	}

	diag.DefaultMonitoring.ActorTimers(actorType, atomic.AddInt64(a.activeTimersCount[actorType], inc))
}

func (a *actorsRuntime) saveActorTypeMetadataRequest(actorType string, actorMetadata *ActorMetadata, stateMetadata map[string]string) state.SetRequest {
	return state.SetRequest{
		Key:      constructCompositeKey("actors", actorType, "metadata"),
		Value:    actorMetadata,
		ETag:     actorMetadata.Etag,
		Metadata: stateMetadata,
		Options: state.SetStateOption{
			Concurrency: state.FirstWrite,
		},
	}
}

func (a *actorsRuntime) getActorTypeMetadata(ctx context.Context, actorType string, migrate bool) (*ActorMetadata, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}

	var policyDef *resiliency.PolicyDefinition
	if !a.resiliency.PolicyDefined(a.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[*ActorMetadata](ctx, policyDef)
	getReq := &state.GetRequest{
		Key: constructCompositeKey("actors", actorType, "metadata"),
		Metadata: map[string]string{
			metadataPartitionKey: constructCompositeKey("actors", actorType),
		},
	}
	return policyRunner(func(ctx context.Context) (*ActorMetadata, error) {
		rResp, rErr := store.Get(ctx, getReq)
		if rErr != nil {
			return nil, rErr
		}
		actorMetadata := &ActorMetadata{
			ID: metadataZeroID,
			RemindersMetadata: ActorRemindersMetadata{
				partitionsEtag: nil,
				PartitionCount: 0,
			},
			Etag: nil,
		}
		if len(rResp.Data) > 0 {
			rErr = json.Unmarshal(rResp.Data, actorMetadata)
			if rErr != nil {
				return nil, fmt.Errorf("could not parse metadata for actor type %s (%s): %w", actorType, string(rResp.Data), rErr)
			}
			actorMetadata.Etag = rResp.ETag
		}

		if migrate && ctx.Err() == nil {
			rErr = a.migrateRemindersForActorType(ctx, store, actorType, actorMetadata)
			if rErr != nil {
				return nil, rErr
			}
		}

		return actorMetadata, nil
	})
}

func (a *actorsRuntime) migrateRemindersForActorType(ctx context.Context, store transactionalStateStore, actorType string, actorMetadata *ActorMetadata) error {
	reminderPartitionCount := a.config.GetRemindersPartitionCountForType(actorType)
	if actorMetadata.RemindersMetadata.PartitionCount == reminderPartitionCount {
		return nil
	}

	if actorMetadata.RemindersMetadata.PartitionCount > reminderPartitionCount {
		log.Warnf("cannot decrease number of partitions for reminders of actor type %s", actorType)
		return nil
	}

	a.remindersStoringLock.Lock()
	defer a.remindersStoringLock.Unlock()

	log.Warnf("migrating actor metadata record for actor type %s", actorType)

	// Fetch all reminders for actor type.
	reminderRefs, refreshedActorMetadata, err := a.getRemindersForActorType(ctx, actorType, false)
	if err != nil {
		return err
	}
	if refreshedActorMetadata.ID != actorMetadata.ID {
		return fmt.Errorf("could not migrate reminders for actor type %s due to race condition in actor metadata", actorType)
	}

	log.Infof("migrating %d reminders for actor type %s", len(reminderRefs), actorType)
	*actorMetadata = *refreshedActorMetadata

	// Recreate as a new metadata identifier.
	idObj, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("failed to generate UUID: %w", err)
	}
	actorMetadata.ID = idObj.String()
	actorMetadata.RemindersMetadata.PartitionCount = reminderPartitionCount
	actorRemindersPartitions := make([][]reminders.Reminder, actorMetadata.RemindersMetadata.PartitionCount)
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		actorRemindersPartitions[i] = make([]reminders.Reminder, 0)
	}

	// Recalculate partition for each reminder.
	for _, reminderRef := range reminderRefs {
		partitionID := actorMetadata.calculateReminderPartition(reminderRef.reminder.ActorID, reminderRef.reminder.Name)
		actorRemindersPartitions[partitionID-1] = append(actorRemindersPartitions[partitionID-1], reminderRef.reminder)
	}

	// Create the requests to put in the transaction.
	stateOperations := make([]state.TransactionalStateOperation, actorMetadata.RemindersMetadata.PartitionCount+1)
	stateMetadata := map[string]string{
		metadataPartitionKey: actorMetadata.ID,
	}
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		stateKey := actorMetadata.calculateRemindersStateKey(actorType, uint32(i+1))
		stateOperations[i] = a.saveRemindersInPartitionRequest(stateKey, actorRemindersPartitions[i], nil, stateMetadata)
	}

	// Also create a request to save the new metadata, so the new "metadataID" becomes the new de facto referenced list for reminders
	stateOperations[len(stateOperations)-1] = a.saveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata)

	// Perform all operations in a transaction
	err = a.executeStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
	if err != nil {
		return fmt.Errorf("failed to perform transaction to migrate records for actor type %s: %w", actorType, err)
	}

	log.Warnf(
		"completed actor metadata record migration for actor type %s, new metadata ID = %s",
		actorType, actorMetadata.ID)
	return nil
}

func (a *actorsRuntime) getRemindersForActorType(ctx context.Context, actorType string, migrate bool) ([]actorReminderReference, *ActorMetadata, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, nil, err
	}

	actorMetadata, err := a.getActorTypeMetadata(ctx, actorType, migrate)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read actor type metadata: %w", err)
	}

	policyDef := a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore)

	log.Debugf(
		"starting to read reminders for actor type %s (migrate=%t), with metadata id %s and %d partitions",
		actorType, migrate, actorMetadata.ID, actorMetadata.RemindersMetadata.PartitionCount)

	if actorMetadata.RemindersMetadata.PartitionCount >= 1 {
		metadata := map[string]string{metadataPartitionKey: actorMetadata.ID}
		actorMetadata.RemindersMetadata.partitionsEtag = map[uint32]*string{}

		keyPartitionMap := make(map[string]uint32, actorMetadata.RemindersMetadata.PartitionCount)
		getRequests := make([]state.GetRequest, actorMetadata.RemindersMetadata.PartitionCount)
		for i := uint32(1); i <= uint32(actorMetadata.RemindersMetadata.PartitionCount); i++ {
			key := actorMetadata.calculateRemindersStateKey(actorType, i)
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

		list := []actorReminderReference{}
		for _, resp := range bulkResponse {
			partition := keyPartitionMap[resp.Key]
			actorMetadata.RemindersMetadata.partitionsEtag[partition] = resp.ETag
			if resp.Error != "" {
				return nil, nil, fmt.Errorf("could not get reminders partition %v: %v", resp.Key, resp.Error)
			}

			var batch []reminders.Reminder
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
			batchList := make([]actorReminderReference, len(batch))
			for j := range batch {
				batchList[j] = actorReminderReference{
					actorMetadataID:           actorMetadata.ID,
					actorRemindersPartitionID: partition,
					reminder:                  batch[j],
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

	var reminders []reminders.Reminder
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &reminders)
		if err != nil {
			return nil, nil, fmt.Errorf("could not parse actor reminders: %w", err)
		}
	}

	reminderRefs := make([]actorReminderReference, len(reminders))
	for j := range reminders {
		reminderRefs[j] = actorReminderReference{
			actorMetadataID:           actorMetadata.ID,
			actorRemindersPartitionID: 0,
			reminder:                  reminders[j],
		}
	}

	actorMetadata.RemindersMetadata.partitionsEtag = map[uint32]*string{
		0: resp.ETag,
	}

	log.Debugf(
		"finished reading reminders for actor type %s (migrate=%t), with metadata id %s and no partitions: total of %d reminders",
		actorType, migrate, actorMetadata.ID, len(reminderRefs))
	return reminderRefs, actorMetadata, nil
}

func (a *actorsRuntime) saveRemindersInPartitionRequest(stateKey string, reminders []reminders.Reminder, etag *string, metadata map[string]string) state.SetRequest {
	return state.SetRequest{
		Key:      stateKey,
		Value:    reminders,
		ETag:     etag,
		Metadata: metadata,
		Options: state.SetStateOption{
			Concurrency: state.FirstWrite,
		},
	}
}

func (a *actorsRuntime) DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error {
	if !a.config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	if !a.waitForEvaluationChan() {
		return errors.New("error deleting reminder: timed out after 5s")
	}

	a.remindersStoringLock.Lock()
	defer a.remindersStoringLock.Unlock()

	return a.doDeleteReminder(ctx, req.ActorType, req.ActorID, req.Name)
}

func (a *actorsRuntime) doDeleteReminder(ctx context.Context, actorType, actorID, name string) error {
	store, err := a.stateStore()
	if err != nil {
		return err
	}

	reminderKey := constructCompositeKey(actorType, actorID, name)

	stop, exists := a.activeReminders.Load(reminderKey)
	if exists {
		log.Debugf("Found reminder with key: %s. Deleting reminder", reminderKey)
		close(stop.(chan struct{}))
		a.activeReminders.Delete(reminderKey)
	}

	var policyDef *resiliency.PolicyDefinition
	if !a.resiliency.PolicyDefined(a.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[bool](ctx, policyDef)
	found, err := policyRunner(func(ctx context.Context) (bool, error) {
		reminders, actorMetadata, rErr := a.getRemindersForActorType(ctx, actorType, false)
		if rErr != nil {
			return false, fmt.Errorf("error obtaining reminders for actor type %s: %w", actorType, rErr)
		}

		// Remove from partition first
		found, remindersInPartition, stateKey, etag := actorMetadata.removeReminderFromPartition(reminders, actorType, actorID, name)

		// If the reminder doesn't exist, stop here
		if !found {
			return false, nil
		}

		// Now, we can remove from the "global" list.
		n := 0
		for _, v := range reminders {
			if v.reminder.ActorType != actorType ||
				v.reminder.ActorID != actorID || v.reminder.Name != name {
				reminders[n] = v
				n++
			}
		}
		reminders = reminders[:n]

		// Get the database partiton key (needed for CosmosDB)
		databasePartitionKey := actorMetadata.calculateDatabasePartitionKey(stateKey)

		// Check if context is still valid
		rErr = ctx.Err()
		if rErr != nil {
			return false, fmt.Errorf("context error before saving reminders: %w", rErr)
		}

		// Save the partition in the database, in a transaction where we also save the metadata.
		// Saving the metadata too avoids a race condition between an update and repartitioning.
		stateMetadata := map[string]string{
			metadataPartitionKey: databasePartitionKey,
		}
		stateOperations := []state.TransactionalStateOperation{
			a.saveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata),
			a.saveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata),
		}
		rErr = a.executeStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
		if rErr != nil {
			return false, fmt.Errorf("error saving reminders partition and metadata: %w", rErr)
		}

		a.remindersLock.Lock()
		diag.DefaultMonitoring.ActorReminders(actorType, int64(len(reminders)))
		a.reminders[actorType] = reminders
		a.remindersLock.Unlock()
		return true, nil
	})
	if err != nil {
		return err
	}
	if !found {
		// Reminder was not found, so nothing to do here
		return nil
	}

	deletePolicyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	deleteReq := &state.DeleteRequest{
		Key: reminderKey,
	}
	_, err = deletePolicyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.Delete(ctx, deleteReq)
	})
	return err
}

// Deprecated: Currently RenameReminder renames by deleting-then-inserting-again.
// This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder
func (a *actorsRuntime) RenameReminder(ctx context.Context, req *RenameReminderRequest) error {
	log.Warn("[DEPRECATION NOTICE] Currently RenameReminder renames by deleting-then-inserting-again. This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder")

	if !a.config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	store, err := a.stateStore()
	if err != nil {
		return err
	}

	if !a.waitForEvaluationChan() {
		return errors.New("error renaming reminder: timed out after 5s")
	}

	a.remindersStoringLock.Lock()
	defer a.remindersStoringLock.Unlock()

	oldReminder, exists := a.getReminder(req.OldName, req.ActorType, req.ActorID)
	if !exists {
		return nil
	}

	// delete old reminder
	err = a.doDeleteReminder(ctx, req.ActorType, req.ActorID, req.OldName)
	if err != nil {
		return err
	}

	reminder := &reminders.Reminder{
		ActorID:        req.ActorID,
		ActorType:      req.ActorType,
		Name:           req.NewName,
		Data:           oldReminder.Data,
		Period:         oldReminder.Period,
		RegisteredTime: oldReminder.RegisteredTime,
		DueTime:        oldReminder.DueTime,
		ExpirationTime: oldReminder.ExpirationTime,
	}

	stop := make(chan struct{})

	err = a.storeReminder(ctx, store, reminder, stop)
	if err != nil {
		return err
	}

	return a.startReminder(reminder, stop)
}

func (a *actorsRuntime) storeReminder(ctx context.Context, store transactionalStateStore, reminder *reminders.Reminder, stopChannel chan struct{}) error {
	// Store the reminder in active reminders list
	reminderKey := reminder.Key()

	a.activeReminders.Store(reminderKey, stopChannel)

	var policyDef *resiliency.PolicyDefinition
	if !a.resiliency.PolicyDefined(a.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx, policyDef)
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		reminders, actorMetadata, rErr := a.getRemindersForActorType(ctx, reminder.ActorType, false)
		if rErr != nil {
			return struct{}{}, fmt.Errorf("error obtaining reminders for actor type %s: %w", reminder.ActorType, rErr)
		}

		// First we add it to the partition list.
		remindersInPartition, reminderRef, stateKey, etag := actorMetadata.insertReminderInPartition(reminders, *reminder)

		// Get the database partition key (needed for CosmosDB)
		databasePartitionKey := actorMetadata.calculateDatabasePartitionKey(stateKey)

		// Now we can add it to the "global" list.
		reminders = append(reminders, reminderRef)

		// Check if context is still valid
		rErr = ctx.Err()
		if rErr != nil {
			return struct{}{}, fmt.Errorf("context error before saving reminders: %w", rErr)
		}

		// Save the partition in the database, in a transaction where we also save the metadata.
		// Saving the metadata too avoids a race condition between an update and repartitioning.
		stateMetadata := map[string]string{
			metadataPartitionKey: databasePartitionKey,
		}
		stateOperations := []state.TransactionalStateOperation{
			a.saveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata),
			a.saveActorTypeMetadataRequest(reminder.ActorType, actorMetadata, stateMetadata),
		}
		rErr = a.executeStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
		if rErr != nil {
			return struct{}{}, fmt.Errorf("error saving reminders partition and metadata: %w", rErr)
		}

		a.remindersLock.Lock()
		diag.DefaultMonitoring.ActorReminders(reminder.ActorType, int64(len(reminders)))
		a.reminders[reminder.ActorType] = reminders
		a.remindersLock.Unlock()
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (a *actorsRuntime) GetReminder(ctx context.Context, req *GetReminderRequest) (*reminders.Reminder, error) {
	if !a.config.HostedActorTypes.IsActorTypeHosted(req.ActorType) {
		return nil, ErrReminderOpActorNotHosted
	}

	list, _, err := a.getRemindersForActorType(ctx, req.ActorType, false)
	if err != nil {
		return nil, err
	}

	for _, r := range list {
		if r.reminder.ActorID == req.ActorID && r.reminder.Name == req.Name {
			return &reminders.Reminder{
				Data:    r.reminder.Data,
				DueTime: r.reminder.DueTime,
				Period:  r.reminder.Period,
			}, nil
		}
	}
	return nil, nil
}

func (a *actorsRuntime) DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error {
	actorKey := constructCompositeKey(req.ActorType, req.ActorID)
	timerKey := constructCompositeKey(actorKey, req.Name)

	stopChan, exists := a.activeTimers.Load(timerKey)
	if exists {
		close(stopChan.(chan struct{}))
		a.activeTimers.Delete(timerKey)
		a.updateActiveTimersCount(req.ActorType, -1)
	}

	return nil
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

		log.Debugf("registering internal actor type: %s", actorType)
		actor.SetActorRuntime(a)
		a.config.HostedActorTypes.AddActorType(actorType)
		if a.placement != nil {
			if err := a.placement.AddHostedActorType(actorType); err != nil {
				return fmt.Errorf("error updating hosted actor types: %s", err)
			}
		}
	}
	return nil
}

func (a *actorsRuntime) GetActiveActorsCount(ctx context.Context) []*runtimev1pb.ActiveActorsCount {
	actorCountMap := make(map[string]int32, len(a.config.HostedActorTypes))
	for actorType := range a.config.HostedActorTypes {
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

func (a *actorsRuntime) stateStore() (transactionalStateStore, error) {
	storeS, ok := a.compStore.GetStateStore(a.storeName)
	if !ok {
		return nil, errors.New(errStateStoreNotFound)
	}

	store, ok := storeS.(transactionalStateStore)
	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		return nil, errors.New(errStateStoreNotConfigured)
	}

	return store, nil
}
