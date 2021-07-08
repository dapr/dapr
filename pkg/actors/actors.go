// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/valyala/fasthttp"
	"go.uber.org/atomic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/channel"
	configuration "github.com/dapr/dapr/pkg/config"
	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/health"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/retry"
)

const (
	daprSeparator        = "||"
	metadataPartitionKey = "partitionKey"
)

var log = logger.NewLogger("dapr.runtime.actor")

var pattern = regexp.MustCompile(`^(R(?P<repetiton>\d+)/)?P((?P<year>\d+)Y)?((?P<month>\d+)M)?((?P<week>\d+)W)?((?P<day>\d+)D)?(T((?P<hour>\d+)H)?((?P<minute>\d+)M)?((?P<second>\d+)S)?)?$`)

// Actors allow calling into virtual actors as well as actor state management.
type Actors interface {
	Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
	Init() error
	Stop()
	GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error)
	TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) error
	GetReminder(ctx context.Context, req *GetReminderRequest) (*Reminder, error)
	CreateReminder(ctx context.Context, req *CreateReminderRequest) error
	DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error
	CreateTimer(ctx context.Context, req *CreateTimerRequest) error
	DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error
	IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool
	GetActiveActorsCount(ctx context.Context) []ActiveActorsCount
}

type actorsRuntime struct {
	appChannel          channel.AppChannel
	store               state.Store
	transactionalStore  state.TransactionalStore
	placement           *internal.ActorPlacement
	grpcConnectionFn    func(ctx context.Context, address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool, customOpts ...grpc.DialOption) (*grpc.ClientConn, error)
	config              Config
	actorsTable         *sync.Map
	activeTimers        *sync.Map
	activeTimersLock    *sync.RWMutex
	activeReminders     *sync.Map
	remindersLock       *sync.RWMutex
	activeRemindersLock *sync.RWMutex
	reminders           map[string][]Reminder
	evaluationLock      *sync.RWMutex
	evaluationBusy      bool
	evaluationChan      chan bool
	appHealthy          *atomic.Bool
	certChain           *dapr_credentials.CertChain
	tracingSpec         configuration.TracingSpec
	reentrancyEnabled   bool
}

// ActiveActorsCount contain actorType and count of actors each type has.
type ActiveActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type actorRepetiton struct {
	value int
	mutex *sync.Mutex
}

func (repetition *actorRepetiton) decrementAttempt() {
	repetition.mutex.Lock()
	defer repetition.mutex.Unlock()
	if repetition.value != -1 {
		repetition.value--
	}
}

func (repetition *actorRepetiton) getValue() int {
	return repetition.value
}

func newActorRepetition(value int) *actorRepetiton {
	return &actorRepetiton{value: value, mutex: &sync.Mutex{}}
}

const (
	incompatibleStateStore = "state store does not support transactions which actors require to save state - please see https://docs.dapr.io/operations/components/setup-state-store/supported-state-stores/"
)

// NewActors create a new actors runtime with given config.
func NewActors(
	stateStore state.Store,
	appChannel channel.AppChannel,
	grpcConnectionFn func(ctx context.Context, address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool, customOpts ...grpc.DialOption) (*grpc.ClientConn, error),
	config Config,
	certChain *dapr_credentials.CertChain,
	tracingSpec configuration.TracingSpec,
	features []configuration.FeatureSpec) Actors {
	var transactionalStore state.TransactionalStore
	if stateStore != nil {
		features := stateStore.Features()
		if state.FeatureETag.IsPresent(features) && state.FeatureTransactional.IsPresent(features) {
			transactionalStore = stateStore.(state.TransactionalStore)
		}
	}

	return &actorsRuntime{
		appChannel:          appChannel,
		config:              config,
		store:               stateStore,
		transactionalStore:  transactionalStore,
		grpcConnectionFn:    grpcConnectionFn,
		actorsTable:         &sync.Map{},
		activeTimers:        &sync.Map{},
		activeTimersLock:    &sync.RWMutex{},
		activeReminders:     &sync.Map{},
		remindersLock:       &sync.RWMutex{},
		activeRemindersLock: &sync.RWMutex{},
		reminders:           map[string][]Reminder{},
		evaluationLock:      &sync.RWMutex{},
		evaluationBusy:      false,
		evaluationChan:      make(chan bool),
		appHealthy:          atomic.NewBool(true),
		certChain:           certChain,
		tracingSpec:         tracingSpec,
		reentrancyEnabled:   configuration.IsFeatureEnabled(features, configuration.ActorRentrancy) && config.Reentrancy.Enabled,
	}
}

func (a *actorsRuntime) Init() error {
	if len(a.config.PlacementAddresses) == 0 {
		return errors.New("actors: couldn't connect to placement service: address is empty")
	}

	if len(a.config.HostedActorTypes) > 0 {
		if a.store == nil {
			log.Warn("actors: state store must be present to initialize the actor runtime")
		} else {
			features := a.store.Features()
			if !state.FeatureETag.IsPresent(features) || !state.FeatureTransactional.IsPresent(features) {
				return errors.New(incompatibleStateStore)
			}
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
	a.startDeactivationTicker(a.config.ActorDeactivationScanInterval, a.config.ActorIdleTimeout)

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

	healthAddress := fmt.Sprintf("%s/healthz", a.appChannel.GetBaseAddress())
	ch := health.StartEndpointHealthCheck(healthAddress, opts...)
	for {
		appHealthy := <-ch
		a.appHealthy.Store(appHealthy)
	}
}

func (a *actorsRuntime) constructCompositeKey(keys ...string) string {
	return strings.Join(keys, daprSeparator)
}

func (a *actorsRuntime) decomposeCompositeKey(compositeKey string) []string {
	return strings.Split(compositeKey, daprSeparator)
}

func (a *actorsRuntime) deactivateActor(actorType, actorID string) error {
	req := invokev1.NewInvokeMethodRequest(fmt.Sprintf("actors/%s/%s", actorType, actorID))
	req.WithHTTPExtension(nethttp.MethodDelete, "")
	req.WithRawData(nil, invokev1.JSONContentType)

	// TODO Propagate context
	ctx := context.Background()
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "invoke")
		return err
	}

	if resp.Status().Code != nethttp.StatusOK {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, fmt.Sprintf("status_code_%d", resp.Status().Code))
		_, body := resp.RawData()
		return errors.Errorf("error from actor service: %s", string(body))
	}

	actorKey := a.constructCompositeKey(actorType, actorID)
	a.actorsTable.Delete(actorKey)
	diag.DefaultMonitoring.ActorDeactivated(actorType)
	log.Debugf("deactivated actor type=%s, id=%s\n", actorType, actorID)

	return nil
}

func (a *actorsRuntime) getActorTypeAndIDFromKey(key string) (string, string) {
	arr := a.decomposeCompositeKey(key)
	return arr[0], arr[1]
}

func (a *actorsRuntime) startDeactivationTicker(interval, actorIdleTimeout time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for t := range ticker.C {
			a.actorsTable.Range(func(key, value interface{}) bool {
				actorInstance := value.(*actor)

				if actorInstance.isBusy() {
					return true
				}

				durationPassed := t.Sub(actorInstance.lastUsedTime)
				if durationPassed >= actorIdleTimeout {
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

func (a *actorsRuntime) Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	a.placement.WaitUntilPlacementTableIsReady()

	actor := req.Actor()
	targetActorAddress, appID := a.placement.LookupActor(actor.GetActorType(), actor.GetActorId())
	if targetActorAddress == "" {
		return nil, errors.Errorf("error finding address for actor type %s with id %s", actor.GetActorType(), actor.GetActorId())
	}

	var resp *invokev1.InvokeMethodResponse
	var err error

	if a.isActorLocal(targetActorAddress, a.config.HostAddress, a.config.Port) {
		resp, err = a.callLocalActor(ctx, req)
	} else {
		resp, err = a.callRemoteActorWithRetry(ctx, retry.DefaultLinearRetryCount, retry.DefaultLinearBackoffInterval, a.callRemoteActor, targetActorAddress, appID, req)
	}

	if err != nil {
		return nil, err
	}
	return resp, nil
}

// callRemoteActorWithRetry will call a remote actor for the specified number of retries and will only retry in the case of transient failures.
func (a *actorsRuntime) callRemoteActorWithRetry(
	ctx context.Context,
	numRetries int,
	backoffInterval time.Duration,
	fn func(ctx context.Context, targetAddress, targetID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error),
	targetAddress, targetID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	for i := 0; i < numRetries; i++ {
		resp, err := fn(ctx, targetAddress, targetID, req)
		if err == nil {
			return resp, nil
		}
		time.Sleep(backoffInterval)

		code := status.Code(err)
		if code == codes.Unavailable || code == codes.Unauthenticated {
			_, err = a.grpcConnectionFn(context.TODO(), targetAddress, targetID, a.config.Namespace, false, true, false)
			if err != nil {
				return nil, err
			}
			continue
		}
		return resp, err
	}
	return nil, errors.Errorf("failed to invoke target %s after %v retries", targetAddress, numRetries)
}

func (a *actorsRuntime) getOrCreateActor(actorType, actorID string) *actor {
	key := a.constructCompositeKey(actorType, actorID)

	// This avoids allocating multiple actor allocations by calling newActor
	// whenever actor is invoked. When storing actor key first, there is a chance to
	// call newActor, but this is trivial.
	val, ok := a.actorsTable.Load(key)
	if !ok {
		val, _ = a.actorsTable.LoadOrStore(key, newActor(actorType, actorID, a.config.Reentrancy.MaxStackDepth))
	}

	return val.(*actor)
}

func (a *actorsRuntime) callLocalActor(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	actorTypeID := req.Actor()

	act := a.getOrCreateActor(actorTypeID.GetActorType(), actorTypeID.GetActorId())

	// Reentrancy to determine how we lock.
	var reentrancyID *string
	if headerValue, ok := req.Metadata()["Dapr-Reentrancy-Id"]; a.reentrancyEnabled && ok {
		reentrancyID = &headerValue.GetValues()[0]
	} else {
		reentrancyHeader := fasthttp.RequestHeader{}
		uuid := uuid.New().String()
		reentrancyHeader.Add("Dapr-Reentrancy-Id", uuid)
		req.AddHeaders(&reentrancyHeader)
		reentrancyID = &uuid
	}

	err := act.lock(reentrancyID)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	defer act.unlock()

	// Replace method to actors method
	req.Message().Method = fmt.Sprintf("actors/%s/%s/method/%s", actorTypeID.GetActorType(), actorTypeID.GetActorId(), req.Message().Method)
	// Original code overrides method with PUT. Why?
	if req.Message().GetHttpExtension() == nil {
		req.WithHTTPExtension(nethttp.MethodPut, "")
	} else {
		req.Message().HttpExtension.Verb = commonv1pb.HTTPExtension_PUT
	}
	resp, err := a.appChannel.InvokeMethod(ctx, req)
	if err != nil {
		return nil, err
	}

	_, respData := resp.RawData()

	if resp.Status().Code != nethttp.StatusOK {
		return nil, errors.Errorf("error from actor service: %s", string(respData))
	}

	return resp, nil
}

func (a *actorsRuntime) callRemoteActor(
	ctx context.Context,
	targetAddress, targetID string,
	req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	conn, err := a.grpcConnectionFn(context.TODO(), targetAddress, targetID, a.config.Namespace, false, false, false)
	if err != nil {
		return nil, err
	}

	span := diag_utils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)
	resp, err := client.CallActor(ctx, req.Proto())
	if err != nil {
		return nil, err
	}

	return invokev1.InternalInvokeResponse(resp)
}

func (a *actorsRuntime) isActorLocal(targetActorAddress, hostAddress string, grpcPort int) bool {
	return strings.Contains(targetActorAddress, "localhost") || strings.Contains(targetActorAddress, "127.0.0.1") ||
		targetActorAddress == fmt.Sprintf("%s:%v", hostAddress, grpcPort)
}

func (a *actorsRuntime) GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error) {
	if a.store == nil {
		return nil, errors.New("actors: state store does not exist or incorrectly configured")
	}

	partitionKey := a.constructCompositeKey(a.config.AppID, req.ActorType, req.ActorID)
	metadata := map[string]string{metadataPartitionKey: partitionKey}

	key := a.constructActorStateKey(req.ActorType, req.ActorID, req.Key)
	resp, err := a.store.Get(&state.GetRequest{
		Key:      key,
		Metadata: metadata,
	})
	if err != nil {
		return nil, err
	}

	return &StateResponse{
		Data: resp.Data,
	}, nil
}

func (a *actorsRuntime) TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) error {
	if a.store == nil || a.transactionalStore == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}
	operations := []state.TransactionalStateOperation{}
	partitionKey := a.constructCompositeKey(a.config.AppID, req.ActorType, req.ActorID)
	metadata := map[string]string{metadataPartitionKey: partitionKey}

	for _, o := range req.Operations {
		switch o.Operation {
		case Upsert:
			var upsert TransactionalUpsert
			err := mapstructure.Decode(o.Request, &upsert)
			if err != nil {
				return err
			}
			key := a.constructActorStateKey(req.ActorType, req.ActorID, upsert.Key)
			operations = append(operations, state.TransactionalStateOperation{
				Request: state.SetRequest{
					Key:      key,
					Value:    upsert.Value,
					Metadata: metadata,
				},
				Operation: state.Upsert,
			})
		case Delete:
			var delete TransactionalDelete
			err := mapstructure.Decode(o.Request, &delete)
			if err != nil {
				return err
			}

			key := a.constructActorStateKey(req.ActorType, req.ActorID, delete.Key)
			operations = append(operations, state.TransactionalStateOperation{
				Request: state.DeleteRequest{
					Key:      key,
					Metadata: metadata,
				},
				Operation: state.Delete,
			})
		default:
			return errors.Errorf("operation type %s not supported", o.Operation)
		}
	}

	err := a.transactionalStore.Multi(&state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   metadata,
	})
	return err
}

func (a *actorsRuntime) IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool {
	key := a.constructCompositeKey(req.ActorType, req.ActorID)
	_, exists := a.actorsTable.Load(key)
	return exists
}

func (a *actorsRuntime) constructActorStateKey(actorType, actorID, key string) string {
	return a.constructCompositeKey(a.config.AppID, actorType, actorID, key)
}

func (a *actorsRuntime) drainRebalancedActors() {
	// visit all currently active actors
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
				// actor has been moved to a different host, deactivate when calls are done
				// cancel any reminders
				reminders := a.reminders[actorType]
				for _, r := range reminders {
					if r.ActorType == actorType && r.ActorID == actorID {
						reminderKey := a.constructCompositeKey(actorKey, r.Name)
						stopChan, exists := a.activeReminders.Load(reminderKey)
						if exists {
							close(stopChan.(chan bool))
							a.activeReminders.Delete(reminderKey)
						}
					}
				}

				actor := value.(*actor)
				if a.config.DrainRebalancedActors {
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
}

func (a *actorsRuntime) evaluateReminders() {
	a.evaluationLock.Lock()
	defer a.evaluationLock.Unlock()

	a.evaluationBusy = true
	a.evaluationChan = make(chan bool)

	var wg sync.WaitGroup
	for _, t := range a.config.HostedActorTypes {
		vals, _, err := a.getRemindersForActorType(t)
		if err != nil {
			log.Errorf("error getting reminders for actor type %s: %s", t, err)
		} else {
			a.remindersLock.Lock()
			a.reminders[t] = vals
			a.remindersLock.Unlock()

			wg.Add(1)
			go func(wg *sync.WaitGroup, reminders []Reminder) {
				defer wg.Done()

				for i := range reminders {
					r := reminders[i] // Make a copy since we will refer to this as a reference in this loop.
					targetActorAddress, _ := a.placement.LookupActor(r.ActorType, r.ActorID)
					if targetActorAddress == "" {
						continue
					}

					if a.isActorLocal(targetActorAddress, a.config.HostAddress, a.config.Port) {
						actorKey := a.constructCompositeKey(r.ActorType, r.ActorID)
						reminderKey := a.constructCompositeKey(actorKey, r.Name)
						_, exists := a.activeReminders.Load(reminderKey)

						if !exists {
							stop := make(chan bool)
							a.activeReminders.Store(reminderKey, stop)
							err := a.startReminder(&r, stop)
							if err != nil {
								log.Errorf("error starting reminder: %s", err)
							}
						}
					}
				}
			}(&wg, vals)
		}
	}
	wg.Wait()
	close(a.evaluationChan)
	a.evaluationBusy = false
}

func (a *actorsRuntime) getReminderTrack(actorKey, name string) (*ReminderTrack, error) {
	if a.store == nil {
		return nil, errors.New("actors: state store does not exist or incorrectly configured")
	}

	resp, err := a.store.Get(&state.GetRequest{
		Key: a.constructCompositeKey(actorKey, name),
	})
	if err != nil {
		return nil, err
	}

	track := ReminderTrack{
		RepetitionLeft: -1,
	}
	json.Unmarshal(resp.Data, &track)
	return &track, nil
}

func (a *actorsRuntime) updateReminderTrack(actorKey, name string, repetition int) error {
	if a.store == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}

	track := ReminderTrack{
		LastFiredTime:  time.Now().UTC().Format(time.RFC3339),
		RepetitionLeft: repetition,
	}

	err := a.store.Set(&state.SetRequest{
		Key:   a.constructCompositeKey(actorKey, name),
		Value: track,
	})
	return err
}

func (a *actorsRuntime) getUpcomingReminderInvokeTime(reminder *Reminder) (time.Time, error) {
	var nextInvokeTime time.Time

	registeredTime, err := time.Parse(time.RFC3339, reminder.RegisteredTime)
	if err != nil {
		return nextInvokeTime, errors.Wrap(err, "error parsing reminder registered time")
	}

	dueTime, err := time.ParseDuration(reminder.DueTime)
	if err != nil {
		return nextInvokeTime, errors.Wrap(err, "error parsing reminder due time")
	}

	key := a.constructCompositeKey(reminder.ActorType, reminder.ActorID)
	track, err := a.getReminderTrack(key, reminder.Name)
	if err != nil {
		return nextInvokeTime, errors.Wrap(err, "error getting reminder track")
	}

	var lastFiredTime time.Time
	if track != nil && track.LastFiredTime != "" {
		lastFiredTime, err = time.Parse(time.RFC3339, track.LastFiredTime)
		if err != nil {
			return nextInvokeTime, errors.Wrap(err, "error parsing reminder last fired time")
		}
	}

	// the first execution reminder task
	if lastFiredTime.IsZero() {
		nextInvokeTime = registeredTime.Add(dueTime)
	} else {
		period, _, err := parseDuration(reminder.Period)
		if err != nil {
			return nextInvokeTime, errors.Wrap(err, "error parsing reminder period")
		}
		nextInvokeTime = lastFiredTime.Add(period)
	}

	return nextInvokeTime, nil
}

func (a *actorsRuntime) startReminder(reminder *Reminder, stopChannel chan bool) error {
	actorKey := a.constructCompositeKey(reminder.ActorType, reminder.ActorID)
	reminderKey := a.constructCompositeKey(actorKey, reminder.Name)
	nextInvokeTime, err := a.getUpcomingReminderInvokeTime(reminder)
	if err != nil {
		return err
	}

	var period time.Duration
	repetitionsLeft := -1
	if reminder.Period != "" {
		period, repetitionsLeft, err = parseDuration(reminder.Period)
		if err != nil {
			log.Errorf("error parsing reminder period %s: %s", reminder.Period, err)
			return err
		}
		log.Debugf("repetitions allowed are %d", repetitionsLeft)
	}

	repetitions := newActorRepetition(repetitionsLeft)
	go func(reminder *Reminder, period time.Duration, repetitions *actorRepetiton, stop chan bool) {
		now := time.Now().UTC()
		initialDuration := nextInvokeTime.Sub(now)
		time.Sleep(initialDuration)

		// Check if reminder is still active
		select {
		case <-stop:
			log.Infof("reminder: %v with parameters: dueTime: %v, period: %v, data: %v has been deleted.", reminderKey, reminder.DueTime, reminder.Period, reminder.Data)
			return
		default:
			break
		}

		err = a.executeReminder(reminder.ActorType, reminder.ActorID, reminder.DueTime, reminder.Period, reminder.Name, repetitions, reminder.Data)
		if err != nil {
			log.Errorf("error executing reminder: %s", err)
		}

		log.Debugf("repetitions left for actors with id %s are %d", reminder.ActorID, repetitions.getValue())

		if reminder.Period != "" {
			_, exists := a.activeReminders.Load(reminderKey)
			if !exists {
				log.Errorf("could not find active reminder with key: %s", reminderKey)
				return
			}

			if quit, _ := a.stopReminderIfRepetitionsOver(reminder.Name, reminder.ActorID, reminder.ActorType, repetitions.getValue()); quit {
				return
			}

			t := a.configureTicker(period)
			go func(ticker *time.Ticker, actorType, actorID, reminder, dueTime, period string, repetition *actorRepetiton, data interface{}) {
				for {
					select {
					case <-ticker.C:

						err := a.executeReminder(actorType, actorID, dueTime, period, reminder, repetition, data)
						if err != nil {
							log.Debugf("error invoking reminder on actor %s: %s", a.constructCompositeKey(actorType, actorID), err)
						} else {
							log.Debugf("executing reminder on actor succedeed; reminders pending for actor %s:  %d", a.constructCompositeKey(actorType, actorID), *repetition)
							if quit, _ := a.stopReminderIfRepetitionsOver(reminder, actorID, actorType, repetition.getValue()); quit {
								return
							}
						}
					case <-stop:
						log.Infof("reminder: %v with parameters: dueTime: %v, period: %v, data: %v has been deleted.", reminderKey, dueTime, period, data)
						return
					}
				}
			}(t, reminder.ActorType, reminder.ActorID, reminder.Name, reminder.DueTime, reminder.Period, repetitions, reminder.Data)
		} else {
			err := a.DeleteReminder(context.TODO(), &DeleteReminderRequest{
				Name:      reminder.Name,
				ActorID:   reminder.ActorID,
				ActorType: reminder.ActorType,
			})
			if err != nil {
				log.Errorf("error deleting reminder: %s", err)
			}
		}
	}(reminder, period, repetitions, stopChannel)

	return nil
}

func (a *actorsRuntime) executeReminder(actorType, actorID, dueTime, period, reminder string, repetition *actorRepetiton, data interface{}) error {
	r := ReminderResponse{
		DueTime: dueTime,
		Period:  period,
		Data:    data,
	}
	b, err := json.Marshal(&r)
	if err != nil {
		return err
	}

	log.Debugf("executing reminder %s for actor type %s with id %s", reminder, actorType, actorID)
	req := invokev1.NewInvokeMethodRequest(fmt.Sprintf("remind/%s", reminder))
	req.WithActor(actorType, actorID)
	req.WithRawData(b, invokev1.JSONContentType)

	_, err = a.callLocalActor(context.Background(), req)
	if err == nil {
		key := a.constructCompositeKey(actorType, actorID)
		repetition.decrementAttempt()
		err = a.updateReminderTrack(key, reminder, repetition.getValue())
	} else {
		log.Errorf("error execution of reminder %s for actor type %s with id %s: %s", reminder, actorType, actorID, err)
	}
	return err
}

func (a *actorsRuntime) reminderRequiresUpdate(req *CreateReminderRequest, reminder *Reminder) bool {
	if reminder.ActorID == req.ActorID && reminder.ActorType == req.ActorType && reminder.Name == req.Name &&
		(reminder.Data != req.Data || reminder.DueTime != req.DueTime || reminder.Period != req.Period) {
		return true
	}

	return false
}

func (a *actorsRuntime) getReminder(req *CreateReminderRequest) (*Reminder, bool) {
	a.remindersLock.RLock()
	reminders := a.reminders[req.ActorType]
	a.remindersLock.RUnlock()

	for _, r := range reminders {
		if r.ActorID == req.ActorID && r.ActorType == req.ActorType && r.Name == req.Name {
			return &r, true
		}
	}

	return nil, false
}

func (a *actorsRuntime) CreateReminder(ctx context.Context, req *CreateReminderRequest) error {
	if a.store == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}

	a.activeRemindersLock.Lock()
	defer a.activeRemindersLock.Unlock()
	r, exists := a.getReminder(req)
	if exists {
		if a.reminderRequiresUpdate(req, r) {
			err := a.DeleteReminder(ctx, &DeleteReminderRequest{
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

	// Store the reminder in active reminders list
	actorKey := a.constructCompositeKey(req.ActorType, req.ActorID)
	reminderKey := a.constructCompositeKey(actorKey, req.Name)
	stop := make(chan bool)
	a.activeReminders.Store(reminderKey, stop)

	if a.evaluationBusy {
		select {
		case <-time.After(time.Second * 5):
			return errors.New("error creating reminder: timed out after 5s")
		case <-a.evaluationChan:
			break
		}
	}

	reminder := Reminder{
		ActorID:        req.ActorID,
		ActorType:      req.ActorType,
		Name:           req.Name,
		Data:           req.Data,
		Period:         req.Period,
		DueTime:        req.DueTime,
		RegisteredTime: time.Now().UTC().Format(time.RFC3339),
	}

	err := backoff.Retry(func() error {
		reminders, remindersEtag, err := a.getRemindersForActorType(req.ActorType)
		if err != nil {
			return err
		}

		reminders = append(reminders, reminder)

		err = a.store.Set(&state.SetRequest{
			Key:   a.constructCompositeKey("actors", req.ActorType),
			Value: reminders,
			ETag:  remindersEtag,
		})
		if err != nil {
			return err
		}

		a.remindersLock.Lock()
		a.reminders[req.ActorType] = reminders
		a.remindersLock.Unlock()
		return nil
	}, backoff.NewExponentialBackOff())
	if err != nil {
		return err
	}

	err = a.startReminder(&reminder, stop)
	if err != nil {
		return err
	}

	return nil
}

func (a *actorsRuntime) CreateTimer(ctx context.Context, req *CreateTimerRequest) error {
	var (
		err             error
		dueTime, period time.Duration
	)
	a.activeTimersLock.Lock()
	defer a.activeTimersLock.Unlock()
	actorKey := a.constructCompositeKey(req.ActorType, req.ActorID)
	timerKey := a.constructCompositeKey(actorKey, req.Name)

	_, exists := a.actorsTable.Load(actorKey)
	if !exists {
		return errors.Errorf("can't create timer for actor %s: actor not activated", actorKey)
	}

	stopChan, exists := a.activeTimers.Load(timerKey)
	if exists {
		close(stopChan.(chan bool))
	}

	if period, err = time.ParseDuration(req.Period); err != nil {
		return err
	}
	if len(req.DueTime) > 0 {
		if dueTime, err = time.ParseDuration(req.DueTime); err != nil {
			return err
		}
	}

	stop := make(chan bool, 1)
	a.activeTimers.Store(timerKey, stop)

	go func(stop chan bool, req *CreateTimerRequest) {
		// Check if timer is still active
		timer := time.NewTimer(dueTime)
		select {
		case <-time.After(dueTime):
			log.Debugf("Time: %v with parameters: DueTime: %v, Period: %v, Data: %v has been overdue.", timerKey, req.DueTime, req.Period, req.Data)
			break
		case <-stop:
			log.Infof("Time: %v with parameters: DueTime: %v, Period: %v, Data: %v has been deleted.", timerKey, req.DueTime, req.Period, req.Data)
			// Stop timer to free resource
			timer.Stop()
			timer = nil
			return
		}

		err := a.executeTimer(req.ActorType, req.ActorID, req.Name, req.DueTime,
			req.Period, req.Callback, req.Data)
		if err != nil {
			log.Errorf("error invoking timer on actor %s: %s", actorKey, err)
		}

		ticker := a.configureTicker(period)
		actorKey := a.constructCompositeKey(req.ActorType, req.ActorID)

		for {
			select {
			case <-ticker.C:
				_, exists := a.actorsTable.Load(actorKey)
				if exists {
					err := a.executeTimer(req.ActorType, req.ActorID, req.Name, req.DueTime,
						req.Period, req.Callback, req.Data)
					if err != nil {
						log.Errorf("error invoking timer on actor %s: %s", actorKey, err)
					}
				} else {
					a.DeleteTimer(ctx, &DeleteTimerRequest{
						Name:      req.Name,
						ActorID:   req.ActorID,
						ActorType: req.ActorType,
					})
				}
			case <-stop:
				return
			}
		}
	}(stop, req)
	return nil
}

func (a *actorsRuntime) configureTicker(d time.Duration) *time.Ticker {
	if d == 0 {
		// NewTicker cannot take in 0.  The ticker is not exact anyways since it fires
		// after "at least" the duration passed in, so we just change 0 to a valid small duration.
		d = 1 * time.Nanosecond
	}

	t := time.NewTicker(d)
	return t
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
	_, err = a.callLocalActor(context.Background(), req)
	if err != nil {
		log.Errorf("error execution of timer %s for actor type %s with id %s: %s", name, actorType, actorID, err)
	}
	return err
}

func (a *actorsRuntime) getRemindersForActorType(actorType string) ([]Reminder, *string, error) {
	if a.store == nil {
		return nil, nil, errors.New("actors: state store does not exist or incorrectly configured")
	}

	key := a.constructCompositeKey("actors", actorType)
	resp, err := a.store.Get(&state.GetRequest{
		Key: key,
	})
	if err != nil {
		return nil, nil, err
	}

	var reminders []Reminder
	json.Unmarshal(resp.Data, &reminders)

	return reminders, resp.ETag, nil
}

func (a *actorsRuntime) DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error {
	if a.store == nil {
		return errors.New("actors: state store does not exist or incorrectly configured")
	}

	if a.evaluationBusy {
		select {
		case <-time.After(time.Second * 5):
			return errors.New("error deleting reminder: timed out after 5s")
		case <-a.evaluationChan:
			break
		}
	}

	key := a.constructCompositeKey("actors", req.ActorType)
	actorKey := a.constructCompositeKey(req.ActorType, req.ActorID)
	reminderKey := a.constructCompositeKey(actorKey, req.Name)

	stop, exists := a.activeReminders.Load(reminderKey)
	if exists {
		log.Infof("Found reminder with key: %v. Deleting reminder", reminderKey)
		close(stop.(chan bool))
		a.activeReminders.Delete(reminderKey)
	}

	err := backoff.Retry(func() error {
		reminders, remindersEtag, err := a.getRemindersForActorType(req.ActorType)
		if err != nil {
			return err
		}

		for i := len(reminders) - 1; i >= 0; i-- {
			if reminders[i].ActorType == req.ActorType && reminders[i].ActorID == req.ActorID && reminders[i].Name == req.Name {
				reminders = append(reminders[:i], reminders[i+1:]...)
			}
		}

		err = a.store.Set(&state.SetRequest{
			Key:   key,
			Value: reminders,
			ETag:  remindersEtag,
		})
		if err != nil {
			return err
		}

		a.remindersLock.Lock()
		a.reminders[req.ActorType] = reminders
		a.remindersLock.Unlock()
		return nil
	}, backoff.NewExponentialBackOff())
	if err != nil {
		return err
	}

	err = a.store.Delete(&state.DeleteRequest{
		Key: reminderKey,
	})
	if err != nil {
		return err
	}

	return nil
}

func (a *actorsRuntime) GetReminder(ctx context.Context, req *GetReminderRequest) (*Reminder, error) {
	reminders, _, err := a.getRemindersForActorType(req.ActorType)
	if err != nil {
		return nil, err
	}

	for _, r := range reminders {
		if r.ActorID == req.ActorID && r.Name == req.Name {
			return &Reminder{
				Data:    r.Data,
				DueTime: r.DueTime,
				Period:  r.Period,
			}, nil
		}
	}
	return nil, nil
}

func (a *actorsRuntime) DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error {
	actorKey := a.constructCompositeKey(req.ActorType, req.ActorID)
	timerKey := a.constructCompositeKey(actorKey, req.Name)

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

// parseDuration creates time.Duration from ISO8601 or time.duration string formats.
func parseDuration(from string) (time.Duration, int, error) {
	var match []string
	if pattern.MatchString(from) {
		match = pattern.FindStringSubmatch(from)
	} else {
		d, err := time.ParseDuration(from)
		return d, -1, err
	}
	duration := time.Duration(0)
	// -1 signifies infinite repetition
	repetition := -1
	for i, name := range pattern.SubexpNames() {
		part := match[i]
		if i == 0 || name == "" || part == "" {
			continue
		}

		val, err := strconv.Atoi(part)
		if err != nil {
			return time.Duration(0), 0, err
		}
		switch name {
		case "year":
			duration += time.Hour * 24 * 365 * time.Duration(val)
		case "month":
			duration += time.Hour * 24 * 30 * time.Duration(val)
		case "week":
			duration += time.Hour * 24 * 7 * time.Duration(val)
		case "day":
			duration += time.Hour * 24 * time.Duration(val)
		case "hour":
			duration += time.Hour * time.Duration(val)
		case "minute":
			duration += time.Minute * time.Duration(val)
		case "second":
			duration += time.Second * time.Duration(val)
		case "repetiton":
			repetition = val
		default:
			return time.Duration(0), -1, fmt.Errorf("unknown field %s", name)
		}
	}

	return duration, repetition, nil
}

func (a *actorsRuntime) stopReminderIfRepetitionsOver(name, actorID, actorType string, repetitionLeft int) (bool, error) {
	if repetitionLeft == 0 {
		err := a.DeleteReminder(context.TODO(), &DeleteReminderRequest{
			Name:      name,
			ActorID:   actorID,
			ActorType: actorType,
		})
		if err != nil {
			log.Errorf("error deleting reminder: %s", err)
		}
		return true, nil
	}
	return false, nil
}
