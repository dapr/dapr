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
	nethttp "net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/concurrency"
	configuration "github.com/dapr/dapr/pkg/config"
	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/retry"
)

const (
	daprSeparator        = "||"
	metadataPartitionKey = "partitionKey"
	metadataZeroID       = "00000000-0000-0000-0000-000000000000"
)

var (
	log     = logger.NewLogger("dapr.runtime.actor")
	pattern = regexp.MustCompile(`^(R(?P<repetition>\d+)/)?P((?P<year>\d+)Y)?((?P<month>\d+)M)?((?P<week>\d+)W)?((?P<day>\d+)D)?(T((?P<hour>\d+)H)?((?P<minute>\d+)M)?((?P<second>\d+)S)?)?$`)
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
	GetReminder(ctx context.Context, req *GetReminderRequest) (*Reminder, error)
	CreateReminder(ctx context.Context, req *CreateReminderRequest) error
	DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error
	RenameReminder(ctx context.Context, req *RenameReminderRequest) error
	CreateTimer(ctx context.Context, req *CreateTimerRequest) error
	DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error
	IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool
	GetActiveActorsCount(ctx context.Context) []ActiveActorsCount
}

// GRPCConnectionFn is the type of the function that returns a gRPC connection
type GRPCConnectionFn func(ctx context.Context, address string, id string, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error)

type actorsRuntime struct {
	appChannel             channel.AppChannel
	store                  state.Store
	transactionalStore     state.TransactionalStore
	placement              *internal.ActorPlacement
	grpcConnectionFn       GRPCConnectionFn
	config                 Config
	actorsTable            *sync.Map
	activeTimers           *sync.Map
	activeTimersLock       *sync.RWMutex
	activeReminders        *sync.Map
	remindersLock          *sync.RWMutex
	remindersMigrationLock *sync.Mutex
	activeRemindersLock    *sync.RWMutex
	reminders              map[string][]actorReminderReference
	evaluationLock         *sync.RWMutex
	evaluationChan         chan struct{}
	appHealthy             *atomic.Bool
	certChain              *daprCredentials.CertChain
	tracingSpec            configuration.TracingSpec
	resiliency             resiliency.Provider
	storeName              string
	isResiliencyEnabled    bool
}

// ActiveActorsCount contain actorType and count of actors each type has.
type ActiveActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
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
	reminder                  Reminder
}

const (
	incompatibleStateStore = "state store does not support transactions which actors require to save state - please see https://docs.dapr.io/operations/components/setup-state-store/supported-state-stores/"
)

var ErrDaprResponseHeader = errors.New("error indicated via actor header response")

// ActorsOpts contains options for NewActors.
type ActorsOpts struct {
	StateStore       state.Store
	AppChannel       channel.AppChannel
	GRPCConnectionFn GRPCConnectionFn
	Config           Config
	CertChain        *daprCredentials.CertChain
	TracingSpec      configuration.TracingSpec
	Features         []configuration.FeatureSpec
	Resiliency       resiliency.Provider
	StateStoreName   string
}

// NewActors create a new actors runtime with given config.
func NewActors(opts ActorsOpts) Actors {
	var transactionalStore state.TransactionalStore
	if opts.StateStore != nil {
		features := opts.StateStore.Features()
		if state.FeatureETag.IsPresent(features) && state.FeatureTransactional.IsPresent(features) {
			transactionalStore = opts.StateStore.(state.TransactionalStore)
		}
	}

	appHealthy := &atomic.Bool{}
	appHealthy.Store(true)
	return &actorsRuntime{
		store:                  opts.StateStore,
		appChannel:             opts.AppChannel,
		grpcConnectionFn:       opts.GRPCConnectionFn,
		config:                 opts.Config,
		certChain:              opts.CertChain,
		tracingSpec:            opts.TracingSpec,
		resiliency:             opts.Resiliency,
		storeName:              opts.StateStoreName,
		transactionalStore:     transactionalStore,
		actorsTable:            &sync.Map{},
		activeTimers:           &sync.Map{},
		activeTimersLock:       &sync.RWMutex{},
		activeReminders:        &sync.Map{},
		remindersLock:          &sync.RWMutex{},
		remindersMigrationLock: &sync.Mutex{},
		activeRemindersLock:    &sync.RWMutex{},
		reminders:              map[string][]actorReminderReference{},
		evaluationLock:         &sync.RWMutex{},
		evaluationChan:         make(chan struct{}, 1),
		appHealthy:             appHealthy,
		isResiliencyEnabled:    configuration.IsFeatureEnabled(opts.Features, configuration.Resiliency),
	}
}

func (a *actorsRuntime) Init() error {
	if len(a.config.PlacementAddresses) == 0 {
		return errors.New("actors: couldn't connect to placement service: address is empty")
	}

	if len(a.config.HostedActorTypes) > 0 {
		if a.store == nil {
			// If we have hosted actors and no store, we can't initialize the actor runtime
			return fmt.Errorf("hosted actors: state store must be present to initialize the actor runtime")
		}

		features := a.store.Features()
		if !state.FeatureETag.IsPresent(features) || !state.FeatureTransactional.IsPresent(features) {
			return errors.New(incompatibleStateStore)
		}
	}

	hostname := fmt.Sprintf("%s:%d", a.config.HostAddress, a.config.Port)

	afterTableUpdateFn := func() {
		a.drainRebalancedActors()
		a.evaluateReminders()
	}
	appHealthFn := func() bool { return a.appHealthy.Load() }

	a.placement = internal.NewActorPlacement(
		a.config.PlacementAddresses, a.certChain,
		a.config.AppID, hostname, a.config.HostedActorTypes,
		appHealthFn,
		afterTableUpdateFn)

	go a.placement.Start()
	a.startDeactivationTicker(a.config)

	log.Infof("actor runtime started. actor idle timeout: %s. actor scan interval: %s",
		a.config.ActorIdleTimeout.String(), a.config.ActorDeactivationScanInterval.String())

	// Be careful to configure healthz endpoint option. If app healthz returns unhealthy status, Dapr will
	// disconnect from placement to remove the node from consistent hashing ring.
	// i.e if app is busy state, the healthz status would be flaky, which leads to frequent
	// actor rebalancing. It will impact the entire service.
	go a.startAppHealthCheck(
		health.WithFailureThreshold(4),
		health.WithInterval(5*time.Second),
		health.WithRequestTimeout(2*time.Second))

	return nil
}

func (a *actorsRuntime) startAppHealthCheck(opts ...health.Option) {
	if len(a.config.HostedActorTypes) == 0 {
		return
	}

	ch := health.StartEndpointHealthCheck(a.appChannel.GetBaseAddress()+"/healthz", opts...)
	for {
		appHealthy := <-ch
		a.appHealthy.Store(appHealthy)
	}
}

func constructCompositeKey(keys ...string) string {
	return strings.Join(keys, daprSeparator)
}

func decomposeCompositeKey(compositeKey string) []string {
	return strings.Split(compositeKey, daprSeparator)
}

func (a *actorsRuntime) deactivateActor(actorType, actorID string) error {
	req := invokev1.NewInvokeMethodRequest(fmt.Sprintf("actors/%s/%s", actorType, actorID))
	req.WithHTTPExtension(nethttp.MethodDelete, "")
	req.WithRawData(nil, invokev1.JSONContentType)

	// TODO Propagate context.
	ctx := context.Background()
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "invoke")
		return err
	}

	if resp.Status().Code != nethttp.StatusOK {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, fmt.Sprintf("status_code_%d", resp.Status().Code))
		_, body := resp.RawData()
		return fmt.Errorf("error from actor service: %s", string(body))
	}

	actorKey := constructCompositeKey(actorType, actorID)
	a.actorsTable.Delete(actorKey)
	diag.DefaultMonitoring.ActorDeactivated(actorType)
	log.Debugf("deactivated actor type=%s, id=%s\n", actorType, actorID)

	return nil
}

func (a *actorsRuntime) getActorTypeAndIDFromKey(key string) (string, string) {
	arr := decomposeCompositeKey(key)
	return arr[0], arr[1]
}

func (a *actorsRuntime) startDeactivationTicker(configuration Config) {
	ticker := time.NewTicker(configuration.ActorDeactivationScanInterval)
	go func() {
		for t := range ticker.C {
			a.actorsTable.Range(func(key, value interface{}) bool {
				actorInstance := value.(*actor)

				if actorInstance.isBusy() {
					return true
				}

				durationPassed := t.Sub(actorInstance.lastUsedTime)
				if durationPassed >= configuration.GetIdleTimeoutForType(actorInstance.actorType) {
					go func(actorKey string) {
						actorType, actorID := a.getActorTypeAndIDFromKey(actorKey)
						err := a.deactivateActor(actorType, actorID)
						if err != nil {
							log.Errorf("failed to deactivate actor %s: %s", actorKey, err)
						}
					}(key.(string))
				}

				return true
			})
		}
	}()
}

type lookupActorRes struct {
	targetActorAddress string
	appID              string
}

func (a *actorsRuntime) Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	a.placement.WaitUntilPlacementTableIsReady()

	actor := req.Actor()
	// Retry here to allow placement table dissemination/rebalancing to happen.
	var policyDef *resiliency.PolicyDefinition
	if a.isResiliencyEnabled {
		policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries)
	} else {
		noOp := resiliency.NoOp{}
		policyDef = noOp.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries)
	}
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
	// TODO: Once resiliency is out of preview, we can have this be the only path.
	if a.isResiliencyEnabled {
		if a.resiliency.GetPolicy(req.Actor().ActorType, &resiliency.ActorPolicy{}) == nil {
			policyRunner := resiliency.NewRunner[*invokev1.InvokeMethodResponse](ctx,
				a.resiliency.BuiltInPolicy(resiliency.BuiltInActorRetries),
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
	for i := 0; i < numRetries; i++ {
		resp, teardown, err := fn(ctx, targetAddress, targetID, req)
		if err == nil {
			teardown(false)
			return resp, nil
		}
		time.Sleep(backoffInterval)

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			// Destroy the connection and force a re-connection on the next attempt
			teardown(true)
			continue
		}

		teardown(false)
		return resp, err
	}
	return nil, fmt.Errorf("failed to invoke target %s after %d retries", targetAddress, numRetries)
}

func (a *actorsRuntime) getOrCreateActor(actorType, actorID string) *actor {
	key := constructCompositeKey(actorType, actorID)

	// This avoids allocating multiple actor allocations by calling newActor
	// whenever actor is invoked. When storing actor key first, there is a chance to
	// call newActor, but this is trivial.
	val, ok := a.actorsTable.Load(key)
	if !ok {
		val, _ = a.actorsTable.LoadOrStore(key, newActor(actorType, actorID, a.config.GetReentrancyForType(actorType).MaxStackDepth))
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
			reentrancyHeader := fasthttp.RequestHeader{}
			uuidObj, err := uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("failed to generate UUID: %w", err)
			}
			uuid := uuidObj.String()
			reentrancyHeader.Add("Dapr-Reentrancy-Id", uuid)
			req.AddHeaders(&reentrancyHeader)
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
	msg.Method = "actors/" + actorTypeID.GetActorType() + "/" + actorTypeID.GetActorId() + "/method/" + msg.Method

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

	policyRunner := resiliency.NewRunner[*invokev1.InvokeMethodResponse](ctx,
		a.resiliency.ActorPostLockPolicy(act.actorType, act.actorID),
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.appChannel.InvokeMethod(ctx, req)
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, errors.New("error from actor service: response object is nil")
	}
	_, respData := resp.RawData()
	if resp.Status().Code != nethttp.StatusOK {
		return nil, fmt.Errorf("error from actor service: %s", string(respData))
	}

	// The .NET SDK signifies Actor failure via a header instead of a bad response.
	if _, ok := resp.Headers()["X-Daprerrorresponseheader"]; ok {
		return resp, ErrDaprResponseHeader
	}

	return resp, nil
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
	resp, err := client.CallActor(ctx, req.Proto())
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
	if a.store == nil {
		return nil, errors.New("actors: state store does not exist or incorrectly configured")
	}

	partitionKey := constructCompositeKey(a.config.AppID, req.ActorType, req.ActorID)
	metadata := map[string]string{metadataPartitionKey: partitionKey}

	key := a.constructActorStateKey(req.ActorType, req.ActorID, req.Key)

	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	storeReq := &state.GetRequest{
		Key:      key,
		Metadata: metadata,
	}
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return a.store.Get(ctx, storeReq)
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
	if a.store == nil || a.transactionalStore == nil {
		return errors.New("actors: state store does not exist or incorrectly configured. Have you set the - name: actorStateStore value: \"true\" in your state store component file?")
	}
	operations := make([]state.TransactionalStateOperation, len(req.Operations))
	partitionKey := constructCompositeKey(a.config.AppID, req.ActorType, req.ActorID)
	metadata := map[string]string{metadataPartitionKey: partitionKey}
	for i, o := range req.Operations {
		switch o.Operation {
		case Upsert:
			var upsert TransactionalUpsert
			err := mapstructure.Decode(o.Request, &upsert)
			if err != nil {
				return err
			}
			key := a.constructActorStateKey(req.ActorType, req.ActorID, upsert.Key)
			operations[i] = state.TransactionalStateOperation{
				Request: state.SetRequest{
					Key:      key,
					Value:    upsert.Value,
					Metadata: metadata,
				},
				Operation: state.Upsert,
			}
		case Delete:
			var delete TransactionalDelete
			err := mapstructure.Decode(o.Request, &delete)
			if err != nil {
				return err
			}
			key := a.constructActorStateKey(req.ActorType, req.ActorID, delete.Key)
			operations[i] = state.TransactionalStateOperation{
				Request: state.DeleteRequest{
					Key:      key,
					Metadata: metadata,
				},
				Operation: state.Delete,
			}
		default:
			return fmt.Errorf("operation type %s not supported", o.Operation)
		}
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	stateReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, a.transactionalStore.Multi(ctx, stateReq)
	})
	return err
}

func (a *actorsRuntime) IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool {
	key := constructCompositeKey(req.ActorType, req.ActorID)
	var policyDef *resiliency.PolicyDefinition
	if a.isResiliencyEnabled {
		policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries)
	} else {
		noOp := resiliency.NoOp{}
		policyDef = noOp.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries)
	}
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

func (a *actorsRuntime) constructActorStateKey(actorType, actorID, key string) string {
	return constructCompositeKey(a.config.AppID, actorType, actorID, key)
}

func (a *actorsRuntime) drainRebalancedActors() {
	// visit all currently active actors.
	var wg sync.WaitGroup

	a.actorsTable.Range(func(key interface{}, value interface{}) bool {
		wg.Add(1)
		go func(key interface{}, value interface{}, wg *sync.WaitGroup) {
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
							close(stopChan.(chan bool))
							a.activeReminders.Delete(reminderKey)
						}
					}
				}

				actor := value.(*actor)
				if a.config.GetDrainRebalancedActorsForType(actorType) {
					// wait until actor isn't busy or timeout hits
					if actor.isBusy() {
						select {
						case <-time.After(a.config.DrainOngoingCallTimeout):
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
					time.Sleep(time.Millisecond * 500)
				}
			}
		}(key, value, &wg)
		return true
	})

	wg.Wait()
}

func (a *actorsRuntime) evaluateReminders() {
	a.evaluationLock.Lock()
	defer a.evaluationLock.Unlock()

	a.evaluationChan <- struct{}{}

	var wg sync.WaitGroup
	for _, t := range a.config.HostedActorTypes {
		vals, _, err := a.getRemindersForActorType(t, true)
		if err != nil {
			log.Errorf("error getting reminders for actor type %s: %s", t, err)
		} else {
			log.Debugf("loaded %d reminders for actor type %s", len(vals), t)
			a.remindersLock.Lock()
			a.reminders[t] = vals
			a.remindersLock.Unlock()

			wg.Add(1)
			go func(wg *sync.WaitGroup, reminders []actorReminderReference) {
				defer wg.Done()

				for i := range reminders {
					r := reminders[i] // Make a copy since we will refer to this as a reference in this loop.
					targetActorAddress, _ := a.placement.LookupActor(r.reminder.ActorType, r.reminder.ActorID)
					if targetActorAddress == "" {
						log.Warnf("did not find address for actor ID %s and actor type %s in reminder %s",
							r.reminder.ActorID,
							r.reminder.ActorType,
							r.reminder.Name)
						continue
					}

					actorKey := constructCompositeKey(r.reminder.ActorType, r.reminder.ActorID)
					reminderKey := constructCompositeKey(actorKey, r.reminder.Name)
					if a.isActorLocal(targetActorAddress, a.config.HostAddress, a.config.Port) {
						_, exists := a.activeReminders.Load(reminderKey)

						if !exists {
							stop := make(chan bool)
							a.activeReminders.Store(reminderKey, stop)
							err := a.startReminder(&r.reminder, stop)
							if err != nil {
								log.Errorf("error starting reminder: %s", err)
							} else {
								log.Debugf("started reminder %s for actor ID %s and actor type %s",
									r.reminder.Name,
									r.reminder.ActorID,
									r.reminder.ActorType)
							}
						} else {
							log.Debugf("reminder %s already exists for actor ID %s and actor type %s",
								r.reminder.Name,
								r.reminder.ActorID,
								r.reminder.ActorType)
						}
					} else {
						stopChan, exists := a.activeReminders.Load(reminderKey)
						if exists {
							log.Debugf("stopping reminder %s on %s as it's active on host %s", reminderKey, a.config.HostAddress, targetActorAddress)
							close(stopChan.(chan bool))
							a.activeReminders.Delete(reminderKey)
						}
					}
				}
			}(&wg, vals)
		}
	}
	wg.Wait()
	<-a.evaluationChan
}

func (a *actorsRuntime) getReminderTrack(actorKey, name string) (*ReminderTrack, error) {
	if a.store == nil {
		return nil, errors.New("actors: state store does not exist or incorrectly configured")
	}

	policyRunner := resiliency.NewRunner[*state.GetResponse](context.TODO(),
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	storeReq := &state.GetRequest{
		Key: constructCompositeKey(actorKey, name),
	}
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return a.store.Get(ctx, storeReq)
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		resp = &state.GetResponse{}
	}
	track := &ReminderTrack{
		RepetitionLeft: -1,
	}
	_ = json.Unmarshal(resp.Data, track)
	track.Etag = resp.ETag
	return track, nil
}

func (a *actorsRuntime) updateReminderTrack(actorKey, name string, repetition int, lastInvokeTime time.Time, etag *string) error {
	if a.store == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}

	track := ReminderTrack{
		LastFiredTime:  lastInvokeTime.Format(time.RFC3339),
		RepetitionLeft: repetition,
	}

	policyRunner := resiliency.NewRunner[any](context.TODO(),
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	setReq := &state.SetRequest{
		Key:   constructCompositeKey(actorKey, name),
		Value: track,
		ETag:  etag,
		Options: state.SetStateOption{
			Concurrency: state.FirstWrite,
		},
	}
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, a.store.Set(ctx, setReq)
	})
	return err
}

func (a *actorsRuntime) startReminder(reminder *Reminder, stopChannel chan bool) error {
	actorKey := constructCompositeKey(reminder.ActorType, reminder.ActorID)
	reminderKey := constructCompositeKey(actorKey, reminder.Name)

	var (
		nextTime, ttl            time.Time
		period                   time.Duration
		years, months, days      int
		repeats, repetitionsLeft int
		eTag                     *string
	)

	registeredTime, err := time.Parse(time.RFC3339, reminder.RegisteredTime)
	if err != nil {
		return fmt.Errorf("error parsing reminder registered time: %w", err)
	}
	if len(reminder.ExpirationTime) != 0 {
		if ttl, err = time.Parse(time.RFC3339, reminder.ExpirationTime); err != nil {
			return fmt.Errorf("error parsing reminder expiration time: %w", err)
		}
	}

	repeats = -1 // set to default
	if len(reminder.Period) != 0 {
		if years, months, days, period, repeats, err = parseDuration(reminder.Period); err != nil {
			return fmt.Errorf("error parsing reminder period: %w", err)
		}
	}

	track, err := a.getReminderTrack(actorKey, reminder.Name)
	if err != nil {
		return fmt.Errorf("error getting reminder track: %w", err)
	}

	if track != nil && len(track.LastFiredTime) != 0 {
		lastFiredTime, err := time.Parse(time.RFC3339, track.LastFiredTime)
		if err != nil {
			return fmt.Errorf("error parsing reminder last fired time: %w", err)
		}

		repetitionsLeft = track.RepetitionLeft
		nextTime = lastFiredTime.AddDate(years, months, days).Add(period)
	} else {
		repetitionsLeft = repeats
		nextTime = registeredTime
	}
	eTag = track.Etag

	go func(reminder *Reminder, years int, months int, days int, period time.Duration, nextTime, ttl time.Time, repetitionsLeft int, eTag *string, stop chan bool) {
		var (
			ttlTimer, nextTimer *time.Timer
			ttlTimerC           <-chan time.Time
			err                 error
		)
		if !ttl.IsZero() {
			ttlTimer = time.NewTimer(time.Until(ttl))
			ttlTimerC = ttlTimer.C
		}
		nextTimer = time.NewTimer(time.Until(nextTime))
		defer func() {
			if nextTimer.Stop() {
				<-nextTimer.C
			}
			if ttlTimer != nil && ttlTimer.Stop() {
				<-ttlTimerC
			}
		}()
	L:
		for {
			select {
			case <-nextTimer.C:
				// noop
			case <-ttlTimerC:
				// proceed with reminder deletion
				log.Infof("reminder %s has expired", reminder.Name)
				break L
			case <-stop:
				// reminder has been already deleted
				log.Infof("reminder %s with parameters: dueTime: %s, period: %s, data: %v has been deleted.", reminder.Name, reminder.RegisteredTime, reminder.Period, reminder.Data)
				return
			}

			_, exists := a.activeReminders.Load(reminderKey)
			if !exists {
				log.Errorf("could not find active reminder with key: %s", reminderKey)
				return
			}
			// if all repetitions are completed, proceed with reminder deletion
			if repetitionsLeft == 0 {
				log.Infof("reminder %q has completed %d repetitions", reminder.Name, repeats)
				break L
			}
			if err = a.executeReminder(reminder); err != nil {
				log.Errorf("error execution of reminder %q for actor type %s with id %s: %v",
					reminder.Name, reminder.ActorType, reminder.ActorID, err)
			}
			if repetitionsLeft > 0 {
				repetitionsLeft--
			}

			_, exists = a.activeReminders.Load(reminderKey)
			if exists {
				if err = a.updateReminderTrack(actorKey, reminder.Name, repetitionsLeft, nextTime, eTag); err != nil {
					log.Errorf("error updating reminder track: %v", err)
				}
				track, gErr := a.getReminderTrack(actorKey, reminder.Name)
				if gErr != nil {
					log.Errorf("error retrieving reminder: %v", gErr)
				} else {
					eTag = track.Etag
				}
			} else {
				log.Errorf("could not find active reminder with key: %s", reminderKey)
				return
			}

			// if reminder is not repetitive, proceed with reminder deletion
			if years == 0 && months == 0 && days == 0 && period == 0 {
				break L
			}
			nextTime = nextTime.AddDate(years, months, days).Add(period)
			if nextTimer.Stop() {
				<-nextTimer.C
			}
			nextTimer.Reset(time.Until(nextTime))
		}
		err = a.DeleteReminder(context.TODO(), &DeleteReminderRequest{
			Name:      reminder.Name,
			ActorID:   reminder.ActorID,
			ActorType: reminder.ActorType,
		})
		if err != nil {
			log.Errorf("error deleting reminder: %s", err)
		}
	}(reminder, years, months, days, period, nextTime, ttl, repetitionsLeft, eTag, stopChannel)

	return nil
}

func (a *actorsRuntime) executeReminder(reminder *Reminder) error {
	r := ReminderResponse{
		DueTime: reminder.DueTime,
		Period:  reminder.Period,
		Data:    reminder.Data,
	}
	b, err := json.Marshal(&r)
	if err != nil {
		return err
	}

	log.Debugf("executing reminder %s for actor type %s with id %s", reminder.Name, reminder.ActorType, reminder.ActorID)
	req := invokev1.NewInvokeMethodRequest(fmt.Sprintf("remind/%s", reminder.Name))
	req.WithActor(reminder.ActorType, reminder.ActorID)
	req.WithRawData(b, invokev1.JSONContentType)

	policyRunner := resiliency.NewRunner[*invokev1.InvokeMethodResponse](context.TODO(),
		a.resiliency.ActorPreLockPolicy(reminder.ActorType, reminder.ActorID),
	)
	_, err = policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.callLocalActor(ctx, req)
	})
	return err
}

func (a *actorsRuntime) reminderRequiresUpdate(req *CreateReminderRequest, reminder *Reminder) bool {
	if reminder.ActorID == req.ActorID && reminder.ActorType == req.ActorType && reminder.Name == req.Name &&
		(!reflect.DeepEqual(reminder.Data, req.Data) || reminder.DueTime != req.DueTime || reminder.Period != req.Period ||
			len(req.TTL) != 0 || (len(reminder.ExpirationTime) != 0 && len(req.TTL) == 0)) {
		return true
	}

	return false
}

func (a *actorsRuntime) getReminder(reminderName string, actorType string, actorID string) (*Reminder, bool) {
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

func (m *ActorMetadata) createReminderReference(reminder Reminder) actorReminderReference {
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

func (m *ActorMetadata) removeReminderFromPartition(reminderRefs []actorReminderReference, actorType, actorID, reminderName string) ([]Reminder, string, *string) {
	// First, we find the partition
	var partitionID uint32
	if m.RemindersMetadata.PartitionCount > 0 {
		for _, reminderRef := range reminderRefs {
			if reminderRef.reminder.ActorType == actorType && reminderRef.reminder.ActorID == actorID && reminderRef.reminder.Name == reminderName {
				partitionID = reminderRef.actorRemindersPartitionID
			}
		}
	}

	var remindersInPartitionAfterRemoval []Reminder
	for _, reminderRef := range reminderRefs {
		if reminderRef.reminder.ActorType == actorType && reminderRef.reminder.ActorID == actorID && reminderRef.reminder.Name == reminderName {
			continue
		}

		// Only the items in the partition to be updated.
		if reminderRef.actorRemindersPartitionID == partitionID {
			remindersInPartitionAfterRemoval = append(remindersInPartitionAfterRemoval, reminderRef.reminder)
		}
	}

	stateKey := m.calculateRemindersStateKey(actorType, partitionID)
	return remindersInPartitionAfterRemoval, stateKey, m.calculateEtag(partitionID)
}

func (m *ActorMetadata) insertReminderInPartition(reminderRefs []actorReminderReference, reminder Reminder) ([]Reminder, actorReminderReference, string, *string) {
	newReminderRef := m.createReminderReference(reminder)

	var remindersInPartitionAfterInsertion []Reminder
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
	t := time.NewTimer(5 * time.Second)
	select {
	case <-t.C:
		return false
	case a.evaluationChan <- struct{}{}:
		t.Stop()
		<-a.evaluationChan
	}
	return true
}

func (a *actorsRuntime) CreateReminder(ctx context.Context, req *CreateReminderRequest) error {
	if a.store == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}

	a.activeRemindersLock.Lock()
	defer a.activeRemindersLock.Unlock()
	if r, exists := a.getReminder(req.Name, req.ActorType, req.ActorID); exists {
		if a.reminderRequiresUpdate(req, r) {
			err := a.doDeleteReminder(ctx, &DeleteReminderRequest{
				ActorID:   req.ActorID,
				ActorType: req.ActorType,
				Name:      req.Name,
			})
			if err != nil {
				return err
			}
		} else {
			return nil
		}
	}

	if !a.waitForEvaluationChan() {
		return errors.New("error creating reminder: timed out after 5s")
	}

	now := time.Now()
	reminder := Reminder{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Name:      req.Name,
		Data:      req.Data,
		Period:    req.Period,
		DueTime:   req.DueTime,
	}

	// check input correctness
	var (
		dueTime, ttl time.Time
		repeats      int
		err          error
	)
	if len(req.DueTime) != 0 {
		if dueTime, err = parseTime(req.DueTime, nil); err != nil {
			return fmt.Errorf("error parsing reminder due time: %w", err)
		}
	} else {
		dueTime = now
	}
	reminder.RegisteredTime = dueTime.Format(time.RFC3339)

	if len(req.Period) != 0 {
		_, _, _, _, repeats, err = parseDuration(req.Period)
		if err != nil {
			return fmt.Errorf("error parsing reminder period: %w", err)
		}
		// error on timers with zero repetitions
		if repeats == 0 {
			return fmt.Errorf("reminder %s has zero repetitions", reminder.Name)
		}
	}
	// set expiration time if configured
	if len(req.TTL) > 0 {
		if ttl, err = parseTime(req.TTL, &dueTime); err != nil {
			return fmt.Errorf("error parsing reminder TTL: %w", err)
		}
		// check if already expired
		if now.After(ttl) || dueTime.After(ttl) {
			return fmt.Errorf("reminder %s has already expired: registeredTime: %s TTL:%s",
				reminder.Name, reminder.RegisteredTime, req.TTL)
		}
		reminder.ExpirationTime = ttl.UTC().Format(time.RFC3339)
	}

	stop := make(chan bool)

	err = a.storeReminder(ctx, reminder, stop)
	if err != nil {
		return err
	}
	return a.startReminder(&reminder, stop)
}

func (a *actorsRuntime) CreateTimer(ctx context.Context, req *CreateTimerRequest) error {
	var (
		err                 error
		repeats             int
		dueTime, ttl        time.Time
		period              time.Duration
		years, months, days int
	)
	a.activeTimersLock.Lock()
	defer a.activeTimersLock.Unlock()
	actorKey := constructCompositeKey(req.ActorType, req.ActorID)
	timerKey := constructCompositeKey(actorKey, req.Name)

	_, exists := a.actorsTable.Load(actorKey)
	if !exists {
		return fmt.Errorf("can't create timer for actor %s: actor not activated", actorKey)
	}

	stopChan, exists := a.activeTimers.Load(timerKey)
	if exists {
		close(stopChan.(chan bool))
	}

	if len(req.DueTime) != 0 {
		if dueTime, err = parseTime(req.DueTime, nil); err != nil {
			return fmt.Errorf("error parsing timer due time: %w", err)
		}
	} else {
		dueTime = time.Now()
	}

	repeats = -1 // set to default
	if len(req.Period) != 0 {
		if years, months, days, period, repeats, err = parseDuration(req.Period); err != nil {
			return fmt.Errorf("error parsing timer period: %w", err)
		}
		// error on timers with zero repetitions
		if repeats == 0 {
			return fmt.Errorf("timer %s has zero repetitions", timerKey)
		}
	}

	if len(req.TTL) > 0 {
		if ttl, err = parseTime(req.TTL, &dueTime); err != nil {
			return fmt.Errorf("error parsing timer TTL: %w", err)
		}
		if time.Now().After(ttl) || dueTime.After(ttl) {
			return fmt.Errorf("timer %s has already expired: dueTime: %s TTL: %s", timerKey, req.DueTime, req.TTL)
		}
	}

	log.Debugf("create timer %q dueTime:%s period:%s repeats:%d ttl:%s",
		req.Name, dueTime.String(), period.String(), repeats, ttl.String())
	stop := make(chan bool, 1)
	a.activeTimers.Store(timerKey, stop)

	go func(stop chan bool, req *CreateTimerRequest) {
		var (
			ttlTimer, nextTimer *time.Timer
			ttlTimerC           <-chan time.Time
			err                 error
		)
		if !ttl.IsZero() {
			ttlTimer = time.NewTimer(time.Until(ttl))
			ttlTimerC = ttlTimer.C
		}
		nextTime := dueTime
		nextTimer = time.NewTimer(time.Until(nextTime))
		defer func() {
			if nextTimer.Stop() {
				<-nextTimer.C
			}
			if ttlTimer != nil && ttlTimer.Stop() {
				<-ttlTimerC
			}
		}()
	L:
		for {
			select {
			case <-nextTimer.C:
				// noop
			case <-ttlTimerC:
				// timer has expired; proceed with deletion
				log.Infof("timer %s with parameters: dueTime: %s, period: %s, TTL: %s, data: %v has expired.", timerKey, req.DueTime, req.Period, req.TTL, req.Data)
				break L
			case <-stop:
				// timer has been already deleted
				log.Infof("timer %s with parameters: dueTime: %s, period: %s, TTL: %s, data: %v has been deleted.", timerKey, req.DueTime, req.Period, req.TTL, req.Data)
				return
			}

			if _, exists := a.actorsTable.Load(actorKey); exists {
				if err = a.executeTimer(req.ActorType, req.ActorID, req.Name, req.DueTime, req.Period, req.Callback, req.Data); err != nil {
					log.Errorf("error invoking timer on actor %s: %s", actorKey, err)
				}
				if repeats > 0 {
					repeats--
				}
			} else {
				log.Errorf("could not find active timer %s", timerKey)
				return
			}
			if repeats == 0 || (years == 0 && months == 0 && days == 0 && period == 0) {
				log.Infof("timer %s has been completed", timerKey)
				break L
			}
			nextTime = nextTime.AddDate(years, months, days).Add(period)
			if nextTimer.Stop() {
				<-nextTimer.C
			}
			nextTimer.Reset(time.Until(nextTime))
		}
		err = a.DeleteTimer(ctx, &DeleteTimerRequest{
			Name:      req.Name,
			ActorID:   req.ActorID,
			ActorType: req.ActorType,
		})
		if err != nil {
			log.Errorf("error deleting timer %s: %v", timerKey, err)
		}
	}(stop, req)
	return nil
}

func (a *actorsRuntime) executeTimer(actorType, actorID, name, dueTime, period, callback string, data interface{}) error {
	t := TimerResponse{
		Callback: callback,
		Data:     data,
		DueTime:  dueTime,
		Period:   period,
	}
	b, err := json.Marshal(&t)
	if err != nil {
		return err
	}

	log.Debugf("executing timer %s for actor type %s with id %s", name, actorType, actorID)
	req := invokev1.NewInvokeMethodRequest(fmt.Sprintf("timer/%s", name))
	req.WithActor(actorType, actorID)
	req.WithRawData(b, invokev1.JSONContentType)

	policyRunner := resiliency.NewRunner[*invokev1.InvokeMethodResponse](context.TODO(),
		a.resiliency.ActorPreLockPolicy(actorType, actorID),
	)
	_, err = policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.callLocalActor(ctx, req)
	})
	if err != nil {
		log.Errorf("error execution of timer %s for actor type %s with id %s: %s", name, actorType, actorID, err)
	}
	return err
}

func (a *actorsRuntime) saveActorTypeMetadata(actorType string, actorMetadata *ActorMetadata) error {
	setReq := &state.SetRequest{
		Key:   constructCompositeKey("actors", actorType, "metadata"),
		Value: actorMetadata,
		ETag:  actorMetadata.Etag,
		Options: state.SetStateOption{
			Concurrency: state.FirstWrite,
		},
	}
	policyRunner := resiliency.NewRunner[any](context.TODO(),
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, a.store.Set(ctx, setReq)
	})
	return err
}

func (a *actorsRuntime) getActorTypeMetadata(actorType string, migrate bool) (result *ActorMetadata, err error) {
	if a.store == nil {
		return nil, errors.New("actors: state store does not exist or incorrectly configured")
	}

	// TODO: Once Resiliency is no longer a preview feature, remove this check and just use resiliency.
	if a.isResiliencyEnabled {
		var policyDef *resiliency.PolicyDefinition
		if a.resiliency.GetPolicy(a.storeName, &resiliency.ComponentOutboundPolicy) == nil {
			// If there is no policy defined, wrap the whole logic in the built-in.
			policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
		} else {
			// Else, we can rely on the underlying operations all being covered by resiliency.
			noOp := resiliency.NoOp{}
			policyDef = noOp.EndpointPolicy("", "")
		}
		policyRunner := resiliency.NewRunner[*ActorMetadata](context.TODO(), policyDef)
		getReq := &state.GetRequest{
			Key: constructCompositeKey("actors", actorType, "metadata"),
		}
		return policyRunner(func(ctx context.Context) (*ActorMetadata, error) {
			rResp, rErr := a.store.Get(ctx, getReq)
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

			if migrate {
				rErr = a.migrateRemindersForActorType(actorType, actorMetadata)
				if rErr != nil {
					return nil, rErr
				}
			}

			return actorMetadata, nil
		})
	}

	return backoff.RetryWithData(func() (*ActorMetadata, error) {
		metadataKey := constructCompositeKey("actors", actorType, "metadata")
		rResp, rErr := a.store.Get(context.TODO(), &state.GetRequest{
			Key: metadataKey,
		})
		if rErr != nil {
			return nil, rErr
		}
		actorMetadata := ActorMetadata{
			ID: metadataZeroID,
			RemindersMetadata: ActorRemindersMetadata{
				partitionsEtag: nil,
				PartitionCount: 0,
			},
			Etag: nil,
		}
		if len(rResp.Data) > 0 {
			rErr = json.Unmarshal(rResp.Data, &actorMetadata)
			if rErr != nil {
				return nil, fmt.Errorf("could not parse metadata for actor type %s (%s): %w", actorType, string(rResp.Data), rErr)
			}
			actorMetadata.Etag = rResp.ETag
		}

		if migrate {
			rErr = a.migrateRemindersForActorType(actorType, &actorMetadata)
			if rErr != nil {
				return nil, rErr
			}
		}

		return &actorMetadata, nil
	}, backoff.NewExponentialBackOff())
}

func (a *actorsRuntime) migrateRemindersForActorType(actorType string, actorMetadata *ActorMetadata) error {
	reminderPartitionCount := a.config.GetRemindersPartitionCountForType(actorType)
	if actorMetadata.RemindersMetadata.PartitionCount == reminderPartitionCount {
		return nil
	}

	if actorMetadata.RemindersMetadata.PartitionCount > reminderPartitionCount {
		log.Warnf("cannot decrease number of partitions for reminders of actor type %s", actorType)
		return nil
	}

	// Nice to have: avoid conflicting migration within the same process.
	a.remindersMigrationLock.Lock()
	defer a.remindersMigrationLock.Unlock()
	log.Warnf("migrating actor metadata record for actor type %s", actorType)

	// Fetch all reminders for actor type.
	reminderRefs, refreshedActorMetadata, err := a.getRemindersForActorType(actorType, false)
	if err != nil {
		return err
	}
	if refreshedActorMetadata.ID != actorMetadata.ID {
		return fmt.Errorf("could not migrate reminders for actor type %s due to race condition in actor metadata", actorType)
	}

	log.Infof("migrating %d reminders for actor type %s", len(reminderRefs), actorType)
	*actorMetadata = *refreshedActorMetadata

	// Recreate as a new metadata identifier.
	actorMetadata.ID = uuid.NewString()
	actorMetadata.RemindersMetadata.PartitionCount = reminderPartitionCount
	actorRemindersPartitions := make([][]Reminder, actorMetadata.RemindersMetadata.PartitionCount)
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		actorRemindersPartitions[i] = make([]Reminder, 0)
	}

	// Recalculate partition for each reminder.
	for _, reminderRef := range reminderRefs {
		partitionID := actorMetadata.calculateReminderPartition(reminderRef.reminder.ActorID, reminderRef.reminder.Name)
		actorRemindersPartitions[partitionID-1] = append(actorRemindersPartitions[partitionID-1], reminderRef.reminder)
	}

	// Save to database.
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		partitionID := i + 1
		stateKey := actorMetadata.calculateRemindersStateKey(actorType, uint32(partitionID))
		stateValue := actorRemindersPartitions[i]
		err = a.saveRemindersInPartition(context.TODO(), stateKey, stateValue, nil, actorMetadata.ID)
		if err != nil {
			return err
		}
	}

	// Save new metadata so the new "metadataID" becomes the new de factor referenced list for reminders.
	err = a.saveActorTypeMetadata(actorType, actorMetadata)
	if err != nil {
		return err
	}
	log.Warnf(
		"completed actor metadata record migration for actor type %s, new metadata ID = %s",
		actorType, actorMetadata.ID)
	return nil
}

type bulkGetRes struct {
	bulkGet      bool
	bulkResponse []state.BulkGetResponse
}

func (a *actorsRuntime) getRemindersForActorType(actorType string, migrate bool) ([]actorReminderReference, *ActorMetadata, error) {
	if a.store == nil {
		return nil, nil, errors.New("actors: state store does not exist or incorrectly configured")
	}

	actorMetadata, merr := a.getActorTypeMetadata(actorType, migrate)
	if merr != nil {
		return nil, nil, fmt.Errorf("could not read actor type metadata: %w", merr)
	}

	policyDef := a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore)

	log.Debugf(
		"starting to read reminders for actor type %s (migrate=%t), with metadata id %s and %d partitions",
		actorType, migrate, actorMetadata.ID, actorMetadata.RemindersMetadata.PartitionCount)
	if actorMetadata.RemindersMetadata.PartitionCount >= 1 {
		metadata := map[string]string{metadataPartitionKey: actorMetadata.ID}
		actorMetadata.RemindersMetadata.partitionsEtag = map[uint32]*string{}
		reminders := []actorReminderReference{}

		keyPartitionMap := map[string]uint32{}
		getRequests := []state.GetRequest{}
		for i := 1; i <= actorMetadata.RemindersMetadata.PartitionCount; i++ {
			partition := uint32(i)
			key := actorMetadata.calculateRemindersStateKey(actorType, partition)
			keyPartitionMap[key] = partition
			getRequests = append(getRequests, state.GetRequest{
				Key:      key,
				Metadata: metadata,
			})
		}

		policyRunner := resiliency.NewRunner[*bulkGetRes](context.TODO(), policyDef)
		bgr, err := policyRunner(func(ctx context.Context) (*bulkGetRes, error) {
			rBulkGet, rBulkResponse, rErr := a.store.BulkGet(ctx, getRequests)
			if rErr != nil {
				return &bulkGetRes{}, rErr
			}
			return &bulkGetRes{
				bulkGet:      rBulkGet,
				bulkResponse: rBulkResponse,
			}, nil
		})
		if bgr == nil {
			bgr = &bulkGetRes{}
		}
		if bgr.bulkGet {
			if err != nil {
				return nil, nil, err
			}
		} else {
			// TODO(artursouza): refactor this fallback into default implementation in contrib.
			// if store doesn't support bulk get, fallback to call get() method one by one
			limiter := concurrency.NewLimiter(actorMetadata.RemindersMetadata.PartitionCount)
			bgr.bulkResponse = make([]state.BulkGetResponse, len(getRequests))
			for i := range getRequests {
				getRequest := getRequests[i]
				bgr.bulkResponse[i].Key = getRequest.Key

				fn := func(param interface{}) {
					r := param.(*state.BulkGetResponse)
					policyRunner := resiliency.NewRunner[*state.GetResponse](context.TODO(), policyDef)
					resp, ferr := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
						return a.store.Get(ctx, &getRequest)
					})
					if ferr != nil {
						r.Error = ferr.Error()
						return
					}

					if resp == nil || len(resp.Data) == 0 {
						r.Error = "data not found for reminder partition"
						return
					}

					r.Data = json.RawMessage(resp.Data)
					r.ETag = resp.ETag
					r.Metadata = resp.Metadata
				}

				limiter.Execute(fn, &bgr.bulkResponse[i])
			}
			limiter.Wait()
		}

		for _, resp := range bgr.bulkResponse {
			partition := keyPartitionMap[resp.Key]
			actorMetadata.RemindersMetadata.partitionsEtag[partition] = resp.ETag
			if resp.Error != "" {
				return nil, nil, fmt.Errorf("could not get reminders partition %v: %v", resp.Key, resp.Error)
			}

			var batch []Reminder
			if len(resp.Data) > 0 {
				err = json.Unmarshal(resp.Data, &batch)
				if err != nil {
					return nil, nil, fmt.Errorf("could not parse actor reminders partition %v: %w", resp.Key, err)
				}
			} else {
				return nil, nil, fmt.Errorf("no data found for reminder partition %v: %w", resp.Key, err)
			}

			for j := range batch {
				reminders = append(reminders, actorReminderReference{
					actorMetadataID:           actorMetadata.ID,
					actorRemindersPartitionID: partition,
					reminder:                  batch[j],
				})
			}
		}

		log.Debugf(
			"finished reading reminders for actor type %s (migrate=%t), with metadata id %s and %d partitions: total of %d reminders",
			actorType, migrate, actorMetadata.ID, actorMetadata.RemindersMetadata.PartitionCount, len(reminders))
		return reminders, actorMetadata, nil
	}

	key := constructCompositeKey("actors", actorType)
	policyRunner := resiliency.NewRunner[*state.GetResponse](context.TODO(), policyDef)
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return a.store.Get(ctx, &state.GetRequest{
			Key: key,
		})
	})
	if err != nil {
		return nil, nil, err
	}

	if resp == nil {
		resp = &state.GetResponse{}
	}
	log.Debugf("read reminders from %s without partition: %s", key, string(resp.Data))

	var reminders []Reminder
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &reminders)
		if err != nil {
			return nil, nil, fmt.Errorf("could not parse actor reminders: %v", err)
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

func (a *actorsRuntime) saveRemindersInPartition(ctx context.Context, stateKey string, reminders []Reminder, etag *string, databasePartitionKey string) error {
	// Even when data is not partitioned, the save operation is the same.
	// The only difference is stateKey.
	log.Debugf("saving %d reminders in %s ...", len(reminders), stateKey)
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	req := &state.SetRequest{
		Key:      stateKey,
		Value:    reminders,
		ETag:     etag,
		Metadata: map[string]string{metadataPartitionKey: databasePartitionKey},
		Options: state.SetStateOption{
			Concurrency: state.FirstWrite,
		},
	}
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, a.store.Set(ctx, req)
	})
	return err
}

func (a *actorsRuntime) DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error {
	a.activeRemindersLock.Lock()
	defer a.activeRemindersLock.Unlock()
	return a.doDeleteReminder(ctx, req)
}

func (a *actorsRuntime) doDeleteReminder(ctx context.Context, req *DeleteReminderRequest) error {
	if a.store == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}

	if !a.waitForEvaluationChan() {
		return errors.New("error deleting reminder: timed out after 5s")
	}

	actorKey := constructCompositeKey(req.ActorType, req.ActorID)
	reminderKey := constructCompositeKey(actorKey, req.Name)

	stop, exists := a.activeReminders.Load(reminderKey)
	if exists {
		log.Infof("Found reminder with key: %v. Deleting reminder", reminderKey)
		close(stop.(chan bool))
		a.activeReminders.Delete(reminderKey)
	}

	var err error
	// TODO: Once Resiliency is no longer a preview feature, remove this check and just use resiliency.
	if a.isResiliencyEnabled {
		var policyDef *resiliency.PolicyDefinition
		if a.resiliency.GetPolicy(a.storeName, &resiliency.ComponentOutboundPolicy) == nil {
			// If there is no policy defined, wrap the whole logic in the built-in.
			policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
		} else {
			// Else, we can rely on the underlying operations all being covered by resiliency.
			noOp := resiliency.NoOp{}
			policyDef = noOp.EndpointPolicy("", "")
		}
		policyRunner := resiliency.NewRunner[any](ctx, policyDef)
		_, err = policyRunner(func(ctx context.Context) (any, error) {
			reminders, actorMetadata, rErr := a.getRemindersForActorType(req.ActorType, false)
			if rErr != nil {
				return nil, rErr
			}

			// remove from partition first.
			remindersInPartition, stateKey, etag := actorMetadata.removeReminderFromPartition(reminders, req.ActorType, req.ActorID, req.Name)

			// now, we can remove from the "global" list.
			for i := len(reminders) - 1; i >= 0; i-- {
				if reminders[i].reminder.ActorType == req.ActorType && reminders[i].reminder.ActorID == req.ActorID && reminders[i].reminder.Name == req.Name {
					reminders = append(reminders[:i], reminders[i+1:]...)
				}
			}

			// Get the database partiton key (needed for CosmosDB)
			databasePartitionKey := actorMetadata.calculateDatabasePartitionKey(stateKey)

			// Then, save the partition to the database.
			rErr = a.saveRemindersInPartition(ctx, stateKey, remindersInPartition, etag, databasePartitionKey)
			if rErr != nil {
				return nil, rErr
			}

			// Finally, we must save metadata to get a new eTag.
			// This avoids a race condition between an update and a repartitioning.
			rErr = a.saveActorTypeMetadata(req.ActorType, actorMetadata)
			if rErr != nil {
				return nil, rErr
			}

			a.remindersLock.Lock()
			a.reminders[req.ActorType] = reminders
			a.remindersLock.Unlock()
			return nil, nil
		})
	} else {
		err = backoff.Retry(func() error {
			reminders, actorMetadata, rErr := a.getRemindersForActorType(req.ActorType, false)
			if rErr != nil {
				return rErr
			}

			// remove from partition first.
			remindersInPartition, stateKey, etag := actorMetadata.removeReminderFromPartition(reminders, req.ActorType, req.ActorID, req.Name)

			// now, we can remove from the "global" list.
			for i := len(reminders) - 1; i >= 0; i-- {
				if reminders[i].reminder.ActorType == req.ActorType && reminders[i].reminder.ActorID == req.ActorID && reminders[i].reminder.Name == req.Name {
					reminders = append(reminders[:i], reminders[i+1:]...)
				}
			}

			// Get the database partiton key (needed for CosmosDB)
			databasePartitionKey := actorMetadata.calculateDatabasePartitionKey(stateKey)

			// Then, save the partition to the database.
			rErr = a.saveRemindersInPartition(ctx, stateKey, remindersInPartition, etag, databasePartitionKey)
			if rErr != nil {
				return rErr
			}

			// Finally, we must save metadata to get a new eTag.
			// This avoids a race condition between an update and a repartitioning.
			rErr = a.saveActorTypeMetadata(req.ActorType, actorMetadata)
			if rErr != nil {
				return rErr
			}

			a.remindersLock.Lock()
			a.reminders[req.ActorType] = reminders
			a.remindersLock.Unlock()
			return nil
		}, backoff.NewExponentialBackOff())
	}

	if err != nil {
		return err
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	deleteReq := &state.DeleteRequest{
		Key: reminderKey,
	}
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, a.store.Delete(ctx, deleteReq)
	})
	return err
}

// Deprecated: Currently RenameReminder renames by deleting-then-inserting-again.
// This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder
func (a *actorsRuntime) RenameReminder(ctx context.Context, req *RenameReminderRequest) error {
	log.Warn("[DEPRECATION NOTICE] Currently RenameReminder renames by deleting-then-inserting-again. This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder")

	if a.store == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}

	a.activeRemindersLock.Lock()
	defer a.activeRemindersLock.Unlock()

	oldReminder, exists := a.getReminder(req.OldName, req.ActorType, req.ActorID)
	if !exists {
		return nil
	}

	// delete old reminder
	err := a.doDeleteReminder(ctx, &DeleteReminderRequest{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Name:      req.OldName,
	})
	if err != nil {
		return err
	}

	if !a.waitForEvaluationChan() {
		return errors.New("error renaming reminder: timed out after 5s")
	}

	reminder := Reminder{
		ActorID:        req.ActorID,
		ActorType:      req.ActorType,
		Name:           req.NewName,
		Data:           oldReminder.Data,
		Period:         oldReminder.Period,
		DueTime:        oldReminder.DueTime,
		RegisteredTime: oldReminder.RegisteredTime,
		ExpirationTime: oldReminder.ExpirationTime,
	}

	stop := make(chan bool)

	err = a.storeReminder(ctx, reminder, stop)
	if err != nil {
		return err
	}

	return a.startReminder(&reminder, stop)
}

func (a *actorsRuntime) storeReminder(ctx context.Context, reminder Reminder, stopChannel chan bool) error {
	// Store the reminder in active reminders list
	actorKey := constructCompositeKey(reminder.ActorType, reminder.ActorID)
	reminderKey := constructCompositeKey(actorKey, reminder.Name)

	a.activeReminders.Store(reminderKey, stopChannel)

	var err error
	// TODO: Once Resiliency is no longer a preview feature, remove this check and just use resiliency.
	if a.isResiliencyEnabled {
		var policyDef *resiliency.PolicyDefinition
		if a.resiliency.GetPolicy(a.storeName, &resiliency.ComponentOutboundPolicy) == nil {
			// If there is no policy defined, wrap the whole logic in the built-in.
			policyDef = a.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
		} else {
			// Else, we can rely on the underlying operations all being covered by resiliency.
			noOp := resiliency.NoOp{}
			policyDef = noOp.EndpointPolicy("", "")
		}
		policyRunner := resiliency.NewRunner[any](ctx, policyDef)
		_, err = policyRunner(func(ctx context.Context) (any, error) {
			reminders, actorMetadata, rErr := a.getRemindersForActorType(reminder.ActorType, false)
			if rErr != nil {
				return nil, rErr
			}

			// First we add it to the partition list.
			remindersInPartition, reminderRef, stateKey, etag := actorMetadata.insertReminderInPartition(reminders, reminder)

			// Get the database partition key (needed for CosmosDB)
			databasePartitionKey := actorMetadata.calculateDatabasePartitionKey(stateKey)

			// Now we can add it to the "global" list.
			reminders = append(reminders, reminderRef)

			// Then, save the partition to the database.
			rErr = a.saveRemindersInPartition(ctx, stateKey, remindersInPartition, etag, databasePartitionKey)
			if rErr != nil {
				return nil, rErr
			}

			// Finally, we must save metadata to get a new eTag.
			// This avoids a race condition between an update and a repartitioning.
			errForSaveMetadata := a.saveActorTypeMetadata(reminder.ActorType, actorMetadata)
			if errForSaveMetadata != nil {
				return nil, errForSaveMetadata
			}

			a.remindersLock.Lock()
			a.reminders[reminder.ActorType] = reminders
			a.remindersLock.Unlock()
			return nil, nil
		})
	} else {
		err = backoff.Retry(func() error {
			reminders, actorMetadata, err2 := a.getRemindersForActorType(reminder.ActorType, false)
			if err2 != nil {
				return err2
			}

			// First we add it to the partition list.
			remindersInPartition, reminderRef, stateKey, etag := actorMetadata.insertReminderInPartition(reminders, reminder)

			// Get the database partition key (needed for CosmosDB)
			databasePartitionKey := actorMetadata.calculateDatabasePartitionKey(stateKey)

			// Now we can add it to the "global" list.
			reminders = append(reminders, reminderRef)

			// Then, save the partition to the database.
			err2 = a.saveRemindersInPartition(ctx, stateKey, remindersInPartition, etag, databasePartitionKey)
			if err2 != nil {
				return err2
			}

			// Finally, we must save metadata to get a new eTag.
			// This avoids a race condition between an update and a repartitioning.
			errForSaveMetadata := a.saveActorTypeMetadata(reminder.ActorType, actorMetadata)
			if errForSaveMetadata != nil {
				return errForSaveMetadata
			}

			a.remindersLock.Lock()
			a.reminders[reminder.ActorType] = reminders
			a.remindersLock.Unlock()
			return nil
		}, backoff.NewExponentialBackOff())
	}

	if err != nil {
		return err
	}
	return nil
}

func (a *actorsRuntime) GetReminder(ctx context.Context, req *GetReminderRequest) (*Reminder, error) {
	reminders, _, err := a.getRemindersForActorType(req.ActorType, false)
	if err != nil {
		return nil, err
	}

	for _, r := range reminders {
		if r.reminder.ActorID == req.ActorID && r.reminder.Name == req.Name {
			return &Reminder{
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
		close(stopChan.(chan bool))
		a.activeTimers.Delete(timerKey)
	}

	return nil
}

func (a *actorsRuntime) GetActiveActorsCount(ctx context.Context) []ActiveActorsCount {
	actorCountMap := map[string]int{}
	for _, actorType := range a.config.HostedActorTypes {
		actorCountMap[actorType] = 0
	}
	a.actorsTable.Range(func(key, value interface{}) bool {
		actorType, _ := a.getActorTypeAndIDFromKey(key.(string))
		actorCountMap[actorType]++
		return true
	})

	activeActorsCount := make([]ActiveActorsCount, 0, len(actorCountMap))
	for actorType, count := range actorCountMap {
		activeActorsCount = append(activeActorsCount, ActiveActorsCount{Type: actorType, Count: count})
	}

	return activeActorsCount
}

// Stop closes all network connections and resources used in actor runtime.
func (a *actorsRuntime) Stop() {
	if a.placement != nil {
		a.placement.Stop()
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

func parseISO8601Duration(from string) (int, int, int, time.Duration, int, error) {
	match := pattern.FindStringSubmatch(from)
	if match == nil {
		return 0, 0, 0, 0, 0, fmt.Errorf("unsupported ISO8601 duration format %q", from)
	}
	years, months, days, duration := 0, 0, 0, time.Duration(0)
	// -1 signifies infinite repetition
	repetition := -1
	for i, name := range pattern.SubexpNames() {
		part := match[i]
		if i == 0 || name == "" || part == "" {
			continue
		}
		val, err := strconv.Atoi(part)
		if err != nil {
			return 0, 0, 0, 0, 0, err
		}
		switch name {
		case "year":
			years = val
		case "month":
			months = val
		case "week":
			days += 7 * val
		case "day":
			days += val
		case "hour":
			duration += time.Hour * time.Duration(val)
		case "minute":
			duration += time.Minute * time.Duration(val)
		case "second":
			duration += time.Second * time.Duration(val)
		case "repetition":
			repetition = val
		default:
			return 0, 0, 0, 0, 0, fmt.Errorf("unsupported ISO8601 duration field %s", name)
		}
	}
	return years, months, days, duration, repetition, nil
}

// parseDuration creates time.Duration from either:
// - ISO8601 duration format,
// - time.Duration string format.
func parseDuration(from string) (int, int, int, time.Duration, int, error) {
	y, m, d, dur, r, err := parseISO8601Duration(from)
	if err == nil {
		return y, m, d, dur, r, nil
	}
	dur, err = time.ParseDuration(from)
	if err == nil {
		return 0, 0, 0, dur, -1, nil
	}
	return 0, 0, 0, 0, 0, fmt.Errorf("unsupported duration format %q", from)
}

// parseTime creates time.Duration from either:
// - ISO8601 duration format,
// - time.Duration string format,
// - RFC3339 datetime format.
// For duration formats, an offset is added.
func parseTime(from string, offset *time.Time) (time.Time, error) {
	var start time.Time
	if offset != nil {
		start = *offset
	} else {
		start = time.Now()
	}
	y, m, d, dur, r, err := parseISO8601Duration(from)
	if err == nil {
		if r != -1 {
			return time.Time{}, errors.New("repetitions are not allowed")
		}
		return start.AddDate(y, m, d).Add(dur), nil
	}
	if dur, err = time.ParseDuration(from); err == nil {
		return start.Add(dur), nil
	}
	if t, err := time.Parse(time.RFC3339, from); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unsupported time/duration format %q", from)
}
