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

package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors/core"
	coreReminder "github.com/dapr/dapr/pkg/actors/core/reminder"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

var log = logger.NewLogger("dapr.runtime.actor")

const (
	metadataPartitionKey = "partitionKey"
	daprSeparator        = "||"
	metadataZeroID       = "00000000-0000-0000-0000-000000000000"
)

var ErrReminderCanceled = errors.New("reminder has been canceled")

type ActorsReminders struct {
	clock                *clock.WithTicker
	remindersStoringLock *sync.Mutex
	resiliency           *resiliency.Provider
	storeName            string
	activeReminders      *sync.Map
	remindersLock        *sync.RWMutex
	reminders            map[string][]core.ActorReminderReference
	ctx                  *context.Context
	evaluationChan       chan struct{}
	config               *core.Config
	stateStoreS          core.TransactionalStateStore
	compStore            *compstore.ComponentStore
	callActorFn          func(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
}

type RemindersOpts struct {
	Clock                *clock.WithTicker
	RemindersStoringLock *sync.Mutex
	Resiliency           *resiliency.Provider
	StateStoreName       string
	ActiveReminders      *sync.Map
	RemindersLock        *sync.RWMutex
	Reminders            map[string][]core.ActorReminderReference
	Ctx                  *context.Context
	EvaluationChan       chan struct{}
	Config               *core.Config
	CompStore            *compstore.ComponentStore
	CallActorFn          func(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
}

func NewReminders(opts RemindersOpts) core.Reminders {
	return newRemindersWithClock(opts)
}

func newRemindersWithClock(opts RemindersOpts) core.Reminders {
	return &ActorsReminders{
		clock:                opts.Clock,
		remindersStoringLock: opts.RemindersStoringLock,
		resiliency:           opts.Resiliency,
		storeName:            opts.StateStoreName,
		activeReminders:      opts.ActiveReminders,
		remindersLock:        opts.RemindersLock,
		reminders:            opts.Reminders,
		callActorFn:          opts.CallActorFn,
		ctx:                  opts.Ctx,
		config:               opts.Config,
		evaluationChan:       opts.EvaluationChan,
		compStore:            opts.CompStore,
	}
}

func (a *ActorsReminders) SetStateStore(store core.TransactionalStateStore) {
	a.stateStoreS = store
}

func (a *ActorsReminders) GetReminder(ctx context.Context, req *coreReminder.GetReminderRequest) (*core.Reminder, error) {
	list, _, err := a.getRemindersForActorType(ctx, req.ActorType, false)
	if err != nil {
		return nil, err
	}

	for _, r := range list {
		if r.Reminder.ActorID == req.ActorID && r.Reminder.Name == req.Name {
			return &core.Reminder{
				Data:    r.Reminder.Data,
				DueTime: r.Reminder.DueTime,
				Period:  r.Reminder.Period,
			}, nil
		}
	}
	return nil, nil
}

func (a *ActorsReminders) CreateReminder(ctx context.Context, req *CreateReminderRequest) error {
	store, err := a.stateStore()
	if err != nil {
		return err
	}
	// Create the new reminder object
	reminder, err := NewReminderFromCreateReminderRequest(req, (*a.clock).Now())
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
	return a.StartReminder(reminder, stop)
}

func (a *ActorsReminders) DeleteReminder(ctx context.Context, req *coreReminder.DeleteReminderRequest) error {
	if !a.waitForEvaluationChan() {
		return errors.New("error deleting reminder: timed out after 5s")
	}

	a.remindersStoringLock.Lock()
	defer a.remindersStoringLock.Unlock()

	return a.doDeleteReminder(ctx, req.ActorType, req.ActorID, req.Name)
}

// Deprecated: Currently RenameReminder renames by deleting-then-inserting-again.
// This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder
func (a *ActorsReminders) RenameReminder(ctx context.Context, req *coreReminder.RenameReminderRequest) error {
	log.Warn("[DEPRECATION NOTICE] Currently RenameReminder renames by deleting-then-inserting-again. This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder")

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

	reminder := &core.Reminder{
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

	return a.StartReminder(reminder, stop)
}

func (a *ActorsReminders) GetReminderTrack(ctx context.Context, key string) (*coreReminder.ReminderTrack, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}

	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx,
		(*a.resiliency).ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
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
	track := &coreReminder.ReminderTrack{
		RepetitionLeft: -1,
	}
	_ = json.Unmarshal(resp.Data, track)
	track.Etag = resp.ETag
	return track, nil
}

func (a *ActorsReminders) StartReminder(reminder *core.Reminder, stopChannel chan struct{}) error {
	reminderKey := reminder.Key()

	track, err := a.GetReminderTrack(context.TODO(), reminderKey)
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
			ttlTimer = (*a.clock).NewTimer(reminder.ExpirationTime.Sub((*a.clock).Now()))
			ttlTimerC = ttlTimer.C()
		}

		nextTimer = (*a.clock).NewTimer(reminder.NextTick().Sub((*a.clock).Now()))
		defer func() {
			if nextTimer.Stop() {
				<-nextTimer.C()
			}
			if ttlTimer != nil && ttlTimer.Stop() {
				<-ttlTimerC
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
				break L
			case <-stopChannel:
				// reminder has been already deleted
				log.Infof("Reminder %s with parameters: dueTime: %s, period: %s has been deleted", reminderKey, reminder.DueTime, reminder.Period)
				return
			}

			_, exists := a.activeReminders.Load(reminderKey)
			if !exists {
				log.Error("Could not find active reminder with key: " + reminderKey)
				return
			}

			// if all repetitions are completed, proceed with reminder deletion
			if reminder.RepeatsLeft() == 0 {
				log.Info("Reminder " + reminderKey + " has been completed")
				break L
			}

			err = a.ExecuteReminder(reminder, false)
			diag.DefaultMonitoring.ActorReminderFired(reminder.ActorType, err == nil)
			if err != nil {
				if errors.Is(err, ErrReminderCanceled) {
					// The handler is explicitly canceling the timer
					log.Debug("Reminder " + reminderKey + " was canceled by the actor")
					break L
				} else {
					log.Errorf("Error while executing reminder %s: %v", reminderKey, err)
				}
			}

			_, exists = a.activeReminders.Load(reminderKey)
			if exists {
				err = a.UpdateReminderTrack(context.TODO(), reminderKey, reminder.RepeatsLeft(), reminder.NextTick(), eTag)
				if err != nil {
					log.Errorf("Error updating reminder track for reminder %s: %v", reminderKey, err)
				}
				track, gErr := a.GetReminderTrack(context.TODO(), reminderKey)
				if gErr != nil {
					log.Errorf("Error retrieving reminder %s: %v", reminderKey, gErr)
				} else {
					eTag = track.Etag
				}
			} else {
				log.Error("Could not find active reminder with key: " + reminderKey)
				return
			}

			if reminder.TickExecuted() {
				break L
			}

			if nextTimer.Stop() {
				<-nextTimer.C()
			}
			nextTimer.Reset(reminder.NextTick().Sub((*a.clock).Now()))
		}

		err = a.DeleteReminder(context.TODO(), &coreReminder.DeleteReminderRequest{
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

func (a *ActorsReminders) UpdateReminderTrack(ctx context.Context, key string, repetition int, lastInvokeTime time.Time, etag *string) error {
	store, err := a.stateStore()
	if err != nil {
		return err
	}

	track := coreReminder.ReminderTrack{
		LastFiredTime:  lastInvokeTime,
		RepetitionLeft: repetition,
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		(*a.resiliency).ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
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

// Executes a reminder or timer
func (a *ActorsReminders) ExecuteReminder(reminder *core.Reminder, isTimer bool) (err error) {
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
		data = &coreReminder.ReminderResponse{
			DueTime: reminder.DueTime,
			Period:  reminder.Period.String(),
			Data:    reminder.Data,
		}
	}
	policyDef := (*a.resiliency).ActorPreLockPolicy(reminder.ActorType, reminder.ActorID)

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
		return a.callActorFn(ctx, req)
	})
	if err != nil && !errors.Is(err, ErrReminderCanceled) {
		log.Errorf("Error executing %s for actor %s: %v", logName, reminder.Key(), err)
	}
	if imr != nil {
		_ = imr.Close()
	}
	return err
}

func (a *ActorsReminders) reminderRequiresUpdate(new *core.Reminder, existing *core.Reminder) bool {
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

func (a *ActorsReminders) getReminder(reminderName string, actorType string, actorID string) (*core.Reminder, bool) {
	a.remindersLock.RLock()
	reminders := a.reminders[actorType]
	a.remindersLock.RUnlock()

	for _, r := range reminders {
		if r.Reminder.ActorID == actorID && r.Reminder.ActorType == actorType && r.Reminder.Name == reminderName {
			return &r.Reminder, true
		}
	}

	return nil, false
}

func (a *ActorsReminders) SaveRemindersInPartitionRequest(stateKey string, reminders []core.Reminder, etag *string, metadata map[string]string) state.SetRequest {
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

func (a *ActorsReminders) SaveActorTypeMetadataRequest(actorType string, actorMetadata *core.ActorMetadata, stateMetadata map[string]string) state.SetRequest {
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

func (a *ActorsReminders) ExecuteStateStoreTransaction(ctx context.Context, store core.TransactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error {
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		(*a.resiliency).ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
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

func (a *ActorsReminders) storeReminder(ctx context.Context, store core.TransactionalStateStore, reminder *core.Reminder, stopChannel chan struct{}) error {
	// Store the reminder in active reminders list
	reminderKey := reminder.Key()

	a.activeReminders.Store(reminderKey, stopChannel)

	var policyDef *resiliency.PolicyDefinition
	if !(*a.resiliency).PolicyDefined(a.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = (*a.resiliency).BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
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
		remindersInPartition, reminderRef, stateKey, etag := actorMetadata.InsertReminderInPartition(reminders, *reminder)

		// Get the database partition key (needed for CosmosDB)
		databasePartitionKey := actorMetadata.CalculateDatabasePartitionKey(stateKey)

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
			a.SaveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata),
			a.SaveActorTypeMetadataRequest(reminder.ActorType, actorMetadata, stateMetadata),
		}
		rErr = a.ExecuteStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
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

func (a *ActorsReminders) waitForEvaluationChan() bool {
	t := (*a.clock).NewTimer(5 * time.Second)
	defer t.Stop()
	select {
	case <-(*a.ctx).Done():
		return false
	case <-t.C():
		return false
	case a.evaluationChan <- struct{}{}:
		<-a.evaluationChan
	}
	return true
}

func constructCompositeKey(keys ...string) string {
	return strings.Join(keys, daprSeparator)
}

func (a *ActorsReminders) GetActorTypeMetadata(ctx context.Context, actorType string, migrate bool) (*core.ActorMetadata, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, err
	}

	var policyDef *resiliency.PolicyDefinition
	if !(*a.resiliency).PolicyDefined(a.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = (*a.resiliency).BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[*core.ActorMetadata](ctx, policyDef)
	getReq := &state.GetRequest{
		Key: constructCompositeKey("actors", actorType, "metadata"),
		Metadata: map[string]string{
			metadataPartitionKey: constructCompositeKey("actors", actorType),
		},
	}
	return policyRunner(func(ctx context.Context) (*core.ActorMetadata, error) {
		rResp, rErr := store.Get(ctx, getReq)
		if rErr != nil {
			return nil, rErr
		}
		actorMetadata := &core.ActorMetadata{
			ID: metadataZeroID,
			RemindersMetadata: core.ActorRemindersMetadata{
				PartitionsEtag: nil,
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

func (a *ActorsReminders) migrateRemindersForActorType(ctx context.Context, store core.TransactionalStateStore, actorType string, actorMetadata *core.ActorMetadata) error {
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
	actorRemindersPartitions := make([][]core.Reminder, actorMetadata.RemindersMetadata.PartitionCount)
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		actorRemindersPartitions[i] = make([]core.Reminder, 0)
	}

	// Recalculate partition for each reminder.
	for _, reminderRef := range reminderRefs {
		partitionID := actorMetadata.CalculateReminderPartition(reminderRef.Reminder.ActorID, reminderRef.Reminder.Name)
		actorRemindersPartitions[partitionID-1] = append(actorRemindersPartitions[partitionID-1], reminderRef.Reminder)
	}

	// Create the requests to put in the transaction.
	stateOperations := make([]state.TransactionalStateOperation, actorMetadata.RemindersMetadata.PartitionCount+1)
	stateMetadata := map[string]string{
		metadataPartitionKey: actorMetadata.ID,
	}
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		stateKey := actorMetadata.CalculateRemindersStateKey(actorType, uint32(i+1))
		stateOperations[i] = a.SaveRemindersInPartitionRequest(stateKey, actorRemindersPartitions[i], nil, stateMetadata)
	}

	// Also create a request to save the new metadata, so the new "metadataID" becomes the new de facto referenced list for reminders
	stateOperations[len(stateOperations)-1] = a.SaveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata)

	// Perform all operations in a transaction
	err = a.ExecuteStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
	if err != nil {
		return fmt.Errorf("failed to perform transaction to migrate records for actor type %s: %w", actorType, err)
	}

	log.Warnf(
		"completed actor metadata record migration for actor type %s, new metadata ID = %s",
		actorType, actorMetadata.ID)
	return nil
}

func (a *ActorsReminders) getRemindersForActorType(ctx context.Context, actorType string, migrate bool) ([]core.ActorReminderReference, *core.ActorMetadata, error) {
	store, err := a.stateStore()
	if err != nil {
		return nil, nil, err
	}

	actorMetadata, err := a.GetActorTypeMetadata(ctx, actorType, migrate)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read actor type metadata: %w", err)
	}

	policyDef := (*a.resiliency).ComponentOutboundPolicy(a.storeName, resiliency.Statestore)

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

func (a *ActorsReminders) doDeleteReminder(ctx context.Context, actorType, actorID, name string) error {
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
	if !(*a.resiliency).PolicyDefined(a.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = (*a.resiliency).BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
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
		found, remindersInPartition, stateKey, etag := actorMetadata.RemoveReminderFromPartition(reminders, actorType, actorID, name)

		// If the reminder doesn't exist, stop here
		if !found {
			return false, nil
		}

		// Now, we can remove from the "global" list.
		n := 0
		for _, v := range reminders {
			if v.Reminder.ActorType != actorType ||
				v.Reminder.ActorID != actorID || v.Reminder.Name != name {
				reminders[n] = v
				n++
			}
		}
		reminders = reminders[:n]

		// Get the database partiton key (needed for CosmosDB)
		databasePartitionKey := actorMetadata.CalculateDatabasePartitionKey(stateKey)

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
			a.SaveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata),
			a.SaveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata),
		}
		rErr = a.ExecuteStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
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
		(*a.resiliency).ComponentOutboundPolicy(a.storeName, resiliency.Statestore),
	)
	deleteReq := &state.DeleteRequest{
		Key: reminderKey,
	}
	_, err = deletePolicyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.Delete(ctx, deleteReq)
	})
	return err
}

func (a *ActorsReminders) stateStore() (core.TransactionalStateStore, error) {
	storeS, ok := a.compStore.GetStateStore(a.storeName)
	if !ok {
		return nil, errors.New(core.ErrStateStoreNotFound)
	}

	store, ok := storeS.(core.TransactionalStateStore)
	if !ok || !state.FeatureETag.IsPresent(store.Features()) || !state.FeatureTransactional.IsPresent(store.Features()) {
		return nil, errors.New(core.ErrStateStoreNotConfigured)
	}
	return store, nil
}
