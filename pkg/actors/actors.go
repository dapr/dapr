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
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/logger"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/mitchellh/mapstructure"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	daprSeparator        = "||"
	metadataPartitionKey = "partitionKey"
)

var log = logger.NewLogger("dapr.runtime.actor")

// Actors allow calling into virtual actors as well as actor state management
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
	grpcConnectionFn    func(address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool) (*grpc.ClientConn, error)
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
	appHealthy          bool
	certChain           *dapr_credentials.CertChain
	tracingSpec         config.TracingSpec
}

// ActiveActorsCount contain actorType and count of actors each type has
type ActiveActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

const (
	incompatibleStateStore = "state store does not support transactions which actors require to save state - please see https://docs.dapr.io/operations/components/setup-state-store/supported-state-stores/"
)

// NewActors create a new actors runtime with given config
func NewActors(
	stateStore state.Store,
	appChannel channel.AppChannel,
	grpcConnectionFn func(address, id string, namespace string, skipTLS, recreateIfExists, enableSSL bool) (*grpc.ClientConn, error),
	config Config,
	certChain *dapr_credentials.CertChain,
	tracingSpec config.TracingSpec) Actors {
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
		appHealthy:          true,
		certChain:           certChain,
		tracingSpec:         tracingSpec,
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
	appHealthFn := func() bool { return a.appHealthy }

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
	// i.e if app is busy state, the heathz status would be flaky, which leads to frequent
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
		a.appHealthy = <-ch
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
							log.Warnf("failed to deactivate actor %s: %s", actorKey, err)
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

// callRemoteActorWithRetry will call a remote actor for the specified number of retries and will only retry in the case of transient failures
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
			_, err = a.grpcConnectionFn(targetAddress, targetID, a.config.Namespace, false, true, false)
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
		val, _ = a.actorsTable.LoadOrStore(key, newActor(actorType, actorID))
	}

	return val.(*actor)
}

func (a *actorsRuntime) callLocalActor(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	actorTypeID := req.Actor()

	act := a.getOrCreateActor(actorTypeID.GetActorType(), actorTypeID.GetActorId())
	err := act.lock()
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
	conn, err := a.grpcConnectionFn(targetAddress, targetID, a.config.Namespace, false, false, false)
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
							log.Warnf("failed to deactivate actor %s: %s", actorKey, err)
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
			log.Debugf("error getting reminders for actor type %s: %s", t, err)
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
								log.Debugf("error starting reminder: %s", err)
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
	resp, err := a.store.Get(&state.GetRequest{
		Key: a.constructCompositeKey(actorKey, name),
	})
	if err != nil {
		return nil, err
	}

	var track ReminderTrack
	json.Unmarshal(resp.Data, &track)
	return &track, nil
}

func (a *actorsRuntime) updateReminderTrack(actorKey, name string) error {
	track := ReminderTrack{
		LastFiredTime: time.Now().UTC().Format(time.RFC3339),
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
		period, err := time.ParseDuration(reminder.Period)
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

	go func(reminder *Reminder, stop chan bool) {
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

		err = a.executeReminder(reminder.ActorType, reminder.ActorID, reminder.DueTime, reminder.Period, reminder.Name, reminder.Data)
		if err != nil {
			log.Errorf("error executing reminder: %s", err)
		}

		if reminder.Period != "" {
			period, err := time.ParseDuration(reminder.Period)
			if err != nil {
				log.Errorf("error parsing reminder period: %s", err)
			}

			_, exists := a.activeReminders.Load(reminderKey)
			if !exists {
				log.Errorf("could not find active reminder with key: %s", reminderKey)
				return
			}

			t := a.configureTicker(period)
			go func(ticker *time.Ticker, actorType, actorID, reminder, dueTime, period string, data interface{}) {
				for {
					select {
					case <-ticker.C:
						err := a.executeReminder(actorType, actorID, dueTime, period, reminder, data)
						if err != nil {
							log.Debugf("error invoking reminder on actor %s: %s", a.constructCompositeKey(actorType, actorID), err)
						}
					case <-stop:
						log.Infof("reminder: %v with parameters: dueTime: %v, period: %v, data: %v has been deleted.", reminderKey, dueTime, period, data)
						return
					}
				}
			}(t, reminder.ActorType, reminder.ActorID, reminder.Name, reminder.DueTime, reminder.Period, reminder.Data)
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
	}(reminder, stopChannel)

	return nil
}

func (a *actorsRuntime) executeReminder(actorType, actorID, dueTime, period, reminder string, data interface{}) error {
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
		err = a.updateReminderTrack(key, reminder)
	} else {
		log.Debugf("error execution of reminder %s for actor type %s with id %s: %s", reminder, actorType, actorID, err)
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

	go func(stop chan (bool), req *CreateTimerRequest) {
		time.Sleep(dueTime)

		// Check if timer is still active
		select {
		case <-stop:
			log.Infof("Time: %v with parameters: DueTime: %v, Period: %v, Data: %v has been deleted.", timerKey, req.DueTime, req.Period, req.Data)
			return
		default:
			break
		}

		err := a.executeTimer(req.ActorType, req.ActorID, req.Name, req.DueTime,
			req.Period, req.Callback, req.Data)
		if err != nil {
			log.Debugf("error invoking timer on actor %s: %s", actorKey, err)
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
						log.Debugf("error invoking timer on actor %s: %s", actorKey, err)
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
		log.Debugf("error execution of timer %s for actor type %s with id %s: %s", name, actorType, actorID, err)
	}
	return err
}

func (a *actorsRuntime) getRemindersForActorType(actorType string) ([]Reminder, *string, error) {
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
	if a.evaluationBusy {
		select {
		case <-time.After(time.Second * 5):
			return errors.New("error creating reminder: timed out after 5s")
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

	activeActorsCount := []ActiveActorsCount{}
	for actorType, count := range actorCountMap {
		activeActorsCount = append(activeActorsCount, ActiveActorsCount{Type: actorType, Count: count})
	}

	return activeActorsCount
}

// Stop closes all network connections and resources used in actor runtime
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
