/*
Copyright 2023 The Dapr Authors
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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/internal"
	daprAppConfig "github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actor.reminders")

const (
	daprSeparator        = "||"
	metadataPartitionKey = "partitionKey"
)

type remindersMetricsCollectorFn = func(actorType string, reminders int64)

// Implements a reminders provider.
type reminders struct {
	clock                clock.WithTicker
	runningCh            chan struct{}
	executeReminderFn    internal.ExecuteReminderFn
	remindersLock        sync.RWMutex
	remindersStoringLock sync.Mutex
	reminders            map[string][]ActorReminderReference
	activeReminders      *sync.Map
	evaluationChan       chan struct{}
	stateStoreProviderFn internal.StateStoreProviderFn
	resiliency           resiliency.Provider
	storeName            string
	evaluationLock       sync.RWMutex
	config               internal.Config
	tracingSpec          daprAppConfig.TracingSpec
	lookUpActorFn        internal.LookupActorFn
	metricsCollector     remindersMetricsCollectorFn
}

// NewRemindersProvider returns a reminders provider.
func NewRemindersProvider(clock clock.WithTicker, opts internal.RemindersProviderOpts) internal.RemindersProvider {
	return &reminders{
		clock:            clock,
		runningCh:        make(chan struct{}),
		reminders:        map[string][]ActorReminderReference{},
		activeReminders:  &sync.Map{},
		evaluationChan:   make(chan struct{}, 1),
		storeName:        opts.StoreName,
		config:           opts.Config,
		metricsCollector: diag.DefaultMonitoring.ActorReminders,
		tracingSpec:      opts.TracingSpec,
	}
}

func (r *reminders) SetExecuteReminderFn(fn internal.ExecuteReminderFn) {
	r.executeReminderFn = fn
}

func (r *reminders) SetStateStoreProviderFn(fn internal.StateStoreProviderFn) {
	r.stateStoreProviderFn = fn
}

func (r *reminders) SetResiliencyProvider(resiliency resiliency.Provider) {
	r.resiliency = resiliency
}

func (r *reminders) SetLookupActorFn(fn internal.LookupActorFn) {
	r.lookUpActorFn = fn
}

func (r *reminders) SetMetricsCollectorFn(fn remindersMetricsCollectorFn) {
	r.metricsCollector = fn
}

// OnPlacementTablesUpdated is invoked when the actors runtime received an updated placement tables.
func (r *reminders) OnPlacementTablesUpdated(ctx context.Context) {
	r.evaluateReminders(ctx)
}

func (r *reminders) DrainRebalancedReminders(actorType string, actorID string) {
	r.remindersLock.RLock()
	reminders := r.reminders[actorType]
	r.remindersLock.RUnlock()

	for _, rem := range reminders {
		// rem.Reminder refers to the actual reminder struct that is saved in the db
		if rem.Reminder.ActorType != actorType || rem.Reminder.ActorID != actorID {
			continue
		}

		reminderKey := rem.Reminder.Key()
		stopChan, exists := r.activeReminders.LoadAndDelete(reminderKey)
		if exists {
			close(stopChan.(chan struct{}))
		}
	}
}

func (r *reminders) CreateReminder(ctx context.Context, reminder *internal.Reminder) error {
	store, err := r.stateStoreProviderFn()
	if err != nil {
		return err
	}
	span := diagUtils.SpanFromContext(ctx)

	if !r.waitForEvaluationChan() {
		return errors.New("error creating reminder: timed out after 5s")
	}

	r.remindersStoringLock.Lock()
	defer r.remindersStoringLock.Unlock()

	existing, ok := r.getReminder(reminder.Name, reminder.ActorType, reminder.ActorID)
	if ok {
		if existing.RequiresUpdating(reminder) {
			err = r.doDeleteReminder(ctx, reminder.ActorType, reminder.ActorID, reminder.Name)
			if err != nil {
				return err
			}
		} else {
			return nil
		}
	}

	stop := make(chan struct{})

	err = r.storeReminder(ctx, store, reminder, stop)
	if err != nil {
		return fmt.Errorf("error storing reminder: %w", err)
	}
	err = r.startReminder(reminder, stop)
	if err != nil {
		return err
	}
	w3cString := diag.SpanContextToW3CString(span.SpanContext())
	return r.updateReminderTrack(ctx, reminder.Key(), reminder.RepeatsLeft(), time.Time{}, nil, w3cString)
}

func (r *reminders) Close() error {
	// Close the runningCh
	close(r.runningCh)
	return nil
}

func (r *reminders) Init(ctx context.Context) error {
	return nil
}

func (r *reminders) GetReminder(ctx context.Context, req *internal.GetReminderRequest) (*internal.Reminder, error) {
	list, _, err := r.getRemindersForActorType(ctx, req.ActorType, false)
	if err != nil {
		return nil, err
	}

	for _, r := range list {
		if r.Reminder.ActorID == req.ActorID && r.Reminder.Name == req.Name {
			return &internal.Reminder{
				Data:    r.Reminder.Data,
				DueTime: r.Reminder.DueTime,
				Period:  r.Reminder.Period,
			}, nil
		}
	}
	return nil, nil
}

func (r *reminders) DeleteReminder(ctx context.Context, req internal.DeleteReminderRequest) error {
	if !r.waitForEvaluationChan() {
		return errors.New("error deleting reminder: timed out after 5s")
	}

	r.remindersStoringLock.Lock()
	defer r.remindersStoringLock.Unlock()

	return r.doDeleteReminder(ctx, req.ActorType, req.ActorID, req.Name)
}

func (r *reminders) RenameReminder(ctx context.Context, req *internal.RenameReminderRequest) error {
	log.Warn("[DEPRECATION NOTICE] Currently RenameReminder renames by deleting-then-inserting-again. This implementation is not fault-tolerant, as a failed insert after deletion would result in no reminder")

	store, err := r.stateStoreProviderFn()
	if err != nil {
		return err
	}

	if !r.waitForEvaluationChan() {
		return errors.New("error renaming reminder: timed out after 5s")
	}

	r.remindersStoringLock.Lock()
	defer r.remindersStoringLock.Unlock()

	oldReminder, exists := r.getReminder(req.OldName, req.ActorType, req.ActorID)
	if !exists {
		return nil
	}

	// delete old reminder
	err = r.doDeleteReminder(ctx, req.ActorType, req.ActorID, req.OldName)
	if err != nil {
		return err
	}

	reminder := &internal.Reminder{
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

	err = r.storeReminder(ctx, store, reminder, stop)
	if err != nil {
		return err
	}

	return r.startReminder(reminder, stop)
}

func (r *reminders) evaluateReminders(ctx context.Context) {
	r.evaluationLock.Lock()
	defer r.evaluationLock.Unlock()

	r.evaluationChan <- struct{}{}

	var wg sync.WaitGroup

	if r.config.HostedActorTypes == nil {
		log.Warn("hostedActorTypes is nil, skipping reminder evaluation")
		<-r.evaluationChan
		return
	}

	for t := range r.config.HostedActorTypes {
		vals, _, err := r.getRemindersForActorType(ctx, t, true)
		if err != nil {
			log.Errorf("Error getting reminders for actor type %s: %s", t, err)
			continue
		}

		log.Debugf("Loaded %d reminders for actor type %s", len(vals), t)
		r.remindersLock.Lock()
		r.reminders[t] = vals
		r.remindersLock.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := range vals {
				rmd := vals[i].Reminder
				reminderKey := rmd.Key()
				isLocalActor, targetActorAddress := r.lookUpActorFn(rmd.ActorType, rmd.ActorID)
				if targetActorAddress == "" {
					log.Warn("Did not find address for actor for reminder " + reminderKey)
					continue
				}

				if isLocalActor {
					stop := make(chan struct{})
					_, exists := r.activeReminders.LoadOrStore(reminderKey, stop)
					if !exists {
						err := r.startReminder(&rmd, stop)
						if err != nil {
							log.Errorf("Error starting reminder %s: %v", reminderKey, err)
						} else {
							log.Debug("Started reminder " + reminderKey)
						}
					} else {
						log.Debug("Reminder " + reminderKey + " already exists")
					}
				} else {
					stopChan, exists := r.activeReminders.LoadAndDelete(reminderKey)
					if exists {
						log.Debugf("Stopping reminder %s on %s as it's active on host %s", reminderKey, r.config.HostAddress, targetActorAddress)
						close(stopChan.(chan struct{}))
					}
				}
			}
		}()
	}
	wg.Wait()
	<-r.evaluationChan
}

func (r *reminders) waitForEvaluationChan() bool {
	t := r.clock.NewTimer(5 * time.Second)
	defer t.Stop()

	select {
	case <-r.runningCh:
		return false
	case <-t.C():
		return false
	case r.evaluationChan <- struct{}{}:
		<-r.evaluationChan
	}
	return true
}

func (r *reminders) getReminder(reminderName string, actorType string, actorID string) (*internal.Reminder, bool) {
	r.remindersLock.RLock()
	reminders := r.reminders[actorType]
	r.remindersLock.RUnlock()

	for _, r := range reminders {
		if r.Reminder.ActorID == actorID && r.Reminder.ActorType == actorType && r.Reminder.Name == reminderName {
			return &r.Reminder, true
		}
	}

	return nil, false
}

func (r *reminders) doDeleteReminder(ctx context.Context, actorType, actorID, name string) error {
	store, err := r.stateStoreProviderFn()
	if err != nil {
		return err
	}

	reminderKey := constructCompositeKey(actorType, actorID, name)

	stop, exists := r.activeReminders.LoadAndDelete(reminderKey)
	if exists {
		log.Debugf("Found reminder with key: %s. Deleting reminder", reminderKey)
		close(stop.(chan struct{}))
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(r.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = r.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[bool](ctx, policyDef)
	found, err := policyRunner(func(ctx context.Context) (bool, error) {
		reminders, actorMetadata, rErr := r.getRemindersForActorType(ctx, actorType, false)
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
			if v.Reminder.ActorType != actorType ||
				v.Reminder.ActorID != actorID || v.Reminder.Name != name {
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
			r.saveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata),
			r.saveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata),
		}
		rErr = r.executeStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
		if rErr != nil {
			return false, fmt.Errorf("error saving reminders partition and metadata: %w", rErr)
		}

		r.remindersLock.Lock()
		r.metricsCollector(actorType, int64(len(reminders)))
		r.reminders[actorType] = reminders
		r.remindersLock.Unlock()
		return true, nil
	})
	if err != nil {
		return err
	}
	if !found {
		// Reminder was not found, so nothing to do here
		return nil
	}

	if r.resiliency != nil && !r.resiliency.PolicyDefined(r.storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(r.storeName, resiliency.Statestore)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	deletePolicyRunner := resiliency.NewRunner[struct{}](ctx, policyDef)
	deleteReq := &state.DeleteRequest{
		Key: reminderKey,
	}
	_, err = deletePolicyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.Delete(ctx, deleteReq)
	})
	return err
}

func (r *reminders) storeReminder(ctx context.Context, store internal.TransactionalStateStore, reminder *internal.Reminder, stopChannel chan struct{}) error {
	// Store the reminder in active reminders list
	reminderKey := reminder.Key()

	_, loaded := r.activeReminders.LoadOrStore(reminderKey, stopChannel)
	if loaded {
		// If the value was loaded, we have a race condition: another goroutine is trying to store the same reminder
		return fmt.Errorf("failed to store reminder %s: reminder was created concurrently by another goroutine", reminderKey)
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(r.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = r.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx, policyDef)
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		reminders, actorMetadata, rErr := r.getRemindersForActorType(ctx, reminder.ActorType, false)
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
			r.saveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata),
			r.saveActorTypeMetadataRequest(reminder.ActorType, actorMetadata, stateMetadata),
		}
		rErr = r.executeStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
		if rErr != nil {
			return struct{}{}, fmt.Errorf("error saving reminders partition and metadata: %w", rErr)
		}

		r.remindersLock.Lock()
		r.metricsCollector(reminder.ActorType, int64(len(reminders)))
		r.reminders[reminder.ActorType] = reminders
		r.remindersLock.Unlock()
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *reminders) executeStateStoreTransaction(ctx context.Context, store internal.TransactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error {
	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(r.storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(r.storeName, resiliency.Statestore)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx, policyDef)
	stateReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.Multi(ctx, stateReq)
	})
	return err
}

func (r *reminders) saveRemindersInPartitionRequest(stateKey string, reminders []internal.Reminder, etag *string, metadata map[string]string) state.SetRequest {
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

func (r *reminders) saveActorTypeMetadataRequest(actorType string, actorMetadata *ActorMetadata, stateMetadata map[string]string) state.SetRequest {
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

func (r *reminders) getRemindersForActorType(ctx context.Context, actorType string, migrate bool) ([]ActorReminderReference, *ActorMetadata, error) {
	store, err := r.stateStoreProviderFn()
	if err != nil {
		return nil, nil, err
	}

	actorMetadata, err := r.getActorTypeMetadata(ctx, actorType, migrate)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read actor type metadata: %w", err)
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && r.resiliency.ComponentOutboundPolicy(r.storeName, resiliency.Statestore) != nil {
		policyDef = r.resiliency.ComponentOutboundPolicy(r.storeName, resiliency.Statestore)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}

	log.Debugf(
		"Starting to read reminders for actor type %s (migrate=%t), with metadata id %s and %d partitions",
		actorType, migrate, actorMetadata.ID, actorMetadata.RemindersMetadata.PartitionCount)

	if actorMetadata.RemindersMetadata.PartitionCount >= 1 {
		metadata := map[string]string{metadataPartitionKey: actorMetadata.ID}
		actorMetadata.RemindersMetadata.PartitionsEtag = map[uint32]*string{}

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

		list := []ActorReminderReference{}
		for _, resp := range bulkResponse {
			partition := keyPartitionMap[resp.Key]
			actorMetadata.RemindersMetadata.PartitionsEtag[partition] = resp.ETag
			if resp.Error != "" {
				return nil, nil, fmt.Errorf("could not get reminders partition %v: %v", resp.Key, resp.Error)
			}

			var batch []internal.Reminder
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
			batchList := make([]ActorReminderReference, len(batch))
			for j := range batch {
				batchList[j] = ActorReminderReference{
					ActorMetadataID:           actorMetadata.ID,
					ActorRemindersPartitionID: partition,
					Reminder:                  batch[j],
				}
			}
			list = append(list, batchList...)
		}

		log.Debugf(
			"Finished reading reminders for actor type %s (migrate=%t), with metadata id %s and %d partitions: total of %d reminders",
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
	log.Debugf("Read reminders from %s without partition", key)

	var reminders []internal.Reminder
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &reminders)
		if err != nil {
			return nil, nil, fmt.Errorf("could not parse actor reminders: %w", err)
		}
	}

	reminderRefs := make([]ActorReminderReference, len(reminders))
	for j := range reminders {
		reminderRefs[j] = ActorReminderReference{
			ActorMetadataID:           actorMetadata.ID,
			ActorRemindersPartitionID: 0,
			Reminder:                  reminders[j],
		}
	}

	actorMetadata.RemindersMetadata.PartitionsEtag = map[uint32]*string{
		0: resp.ETag,
	}

	log.Debugf(
		"Finished reading reminders for actor type %s (migrate=%t), with metadata id %s and no partitions: total of %d reminders",
		actorType, migrate, actorMetadata.ID, len(reminderRefs))
	return reminderRefs, actorMetadata, nil
}

func (r *reminders) getActorTypeMetadata(ctx context.Context, actorType string, migrate bool) (*ActorMetadata, error) {
	store, err := r.stateStoreProviderFn()
	if err != nil {
		return nil, err
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && r.resiliency.PolicyDefined(r.storeName, resiliency.ComponentOutboundPolicy) {
		// If there is no policy defined, wrap the whole logic in the built-in.
		policyDef = r.resiliency.BuiltInPolicy(resiliency.BuiltInActorReminderRetries)
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
			ID: uuid.Nil.String(),
			RemindersMetadata: ActorRemindersMetadata{
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
			rErr = r.migrateRemindersForActorType(ctx, store, actorType, actorMetadata)
			if rErr != nil {
				return nil, rErr
			}
		}

		return actorMetadata, nil
	})
}

func (r *reminders) migrateRemindersForActorType(ctx context.Context, store internal.TransactionalStateStore, actorType string, actorMetadata *ActorMetadata) error {
	reminderPartitionCount := r.config.GetRemindersPartitionCountForType(actorType)
	if actorMetadata.RemindersMetadata.PartitionCount == reminderPartitionCount {
		return nil
	}

	if actorMetadata.RemindersMetadata.PartitionCount > reminderPartitionCount {
		log.Warnf("cannot decrease number of partitions for reminders of actor type %s", actorType)
		return nil
	}

	r.remindersStoringLock.Lock()
	defer r.remindersStoringLock.Unlock()

	log.Warnf("migrating actor metadata record for actor type %s", actorType)

	// Fetch all reminders for actor type.
	reminderRefs, refreshedActorMetadata, err := r.getRemindersForActorType(ctx, actorType, false)
	if err != nil {
		return err
	}
	if refreshedActorMetadata.ID != actorMetadata.ID {
		return fmt.Errorf("could not migrate reminders for actor type %s due to race condition in actor metadata", actorType)
	}

	log.Infof("Migrating %d reminders for actor type %s", len(reminderRefs), actorType)
	*actorMetadata = *refreshedActorMetadata

	// Recreate as a new metadata identifier.
	idObj, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("failed to generate UUID: %w", err)
	}
	actorMetadata.ID = idObj.String()
	actorMetadata.RemindersMetadata.PartitionCount = reminderPartitionCount
	actorRemindersPartitions := make([][]internal.Reminder, actorMetadata.RemindersMetadata.PartitionCount)
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		actorRemindersPartitions[i] = make([]internal.Reminder, 0)
	}

	// Recalculate partition for each reminder.
	for _, reminderRef := range reminderRefs {
		partitionID := actorMetadata.calculateReminderPartition(reminderRef.Reminder.ActorID, reminderRef.Reminder.Name)
		actorRemindersPartitions[partitionID-1] = append(actorRemindersPartitions[partitionID-1], reminderRef.Reminder)
	}

	// Create the requests to put in the transaction.
	stateOperations := make([]state.TransactionalStateOperation, actorMetadata.RemindersMetadata.PartitionCount+1)
	stateMetadata := map[string]string{
		metadataPartitionKey: actorMetadata.ID,
	}
	for i := 0; i < actorMetadata.RemindersMetadata.PartitionCount; i++ {
		stateKey := actorMetadata.calculateRemindersStateKey(actorType, uint32(i+1))
		stateOperations[i] = r.saveRemindersInPartitionRequest(stateKey, actorRemindersPartitions[i], nil, stateMetadata)
	}

	// Also create a request to save the new metadata, so the new "metadataID" becomes the new de facto referenced list for reminders
	stateOperations[len(stateOperations)-1] = r.saveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata)

	// Perform all operations in a transaction
	err = r.executeStateStoreTransaction(ctx, store, stateOperations, stateMetadata)
	if err != nil {
		return fmt.Errorf("failed to perform transaction to migrate records for actor type %s: %w", actorType, err)
	}

	log.Warnf(
		"Completed actor metadata record migration for actor type %s, new metadata ID = %s",
		actorType, actorMetadata.ID)
	return nil
}

func constructCompositeKey(keys ...string) string {
	return strings.Join(keys, daprSeparator)
}

func (r *reminders) startReminder(reminder *internal.Reminder, stopChannel chan struct{}) error {
	reminderKey := reminder.Key()

	track, err := r.getReminderTrack(context.TODO(), reminderKey)
	if err != nil {
		return fmt.Errorf("error getting reminder track: %w", err)
	}

	reminder.UpdateFromTrack(track)

	go func() {
		var (
			nextTimer clock.Timer
			err       error
		)
		eTag := track.Etag

		nextTick, active := reminder.NextTick()
		if !active {
			log.Infof("Reminder %s has expired", reminderKey)
			goto delete
		}

		nextTimer = r.clock.NewTimer(nextTick.Sub(r.clock.Now()))
		defer func() {
			if nextTimer != nil && !nextTimer.Stop() {
				<-nextTimer.C()
			}
		}()

	loop:
		for {
			select {
			case <-nextTimer.C():
				log.Infof("Reminder %s with parameters: dueTime: %s, period: %s is due", reminderKey, reminder.DueTime, reminder.Period)
				// noop
			case <-stopChannel:
				// reminder has been already deleted
				log.Infof("Reminder %s with parameters: dueTime: %s, period: %s has been deleted", reminderKey, reminder.DueTime, reminder.Period)
				return
			case <-r.runningCh:
				// Reminders runtime is stopping
				return
			}

			_, exists := r.activeReminders.Load(reminderKey)
			if !exists {
				log.Error("Could not find active reminder with key: " + reminderKey)
				nextTimer = nil
				return
			}
			track, gErr := r.getReminderTrack(context.TODO(), reminderKey)
			if gErr != nil {
				log.Errorf("Error retrieving reminder %s: %v", reminderKey, gErr)
			}
			var span trace.Span
			if track.TraceState != "" {
				sc, _ := diag.SpanContextFromW3CString(track.TraceState)
				_, span = diag.StartChildSpanCorrelatedToParent(context.Background(), "actors/"+reminderKey, sc, &r.tracingSpec)
			}

			// If all repetitions are completed, delete the reminder and do not execute it
			if reminder.RepeatsLeft() == 0 {
				log.Info("Reminder " + reminderKey + " has been completed")
				nextTimer = nil
				break loop
			}

			if r.executeReminderFn != nil && !r.executeReminderFn(reminder) {
				nextTimer = nil
				break loop
			}

			_, exists = r.activeReminders.Load(reminderKey)
			if exists {
				err = r.updateReminderTrack(context.TODO(), reminderKey, reminder.RepeatsLeft(), nextTick, eTag, track.TraceState)
				if err != nil {
					log.Errorf("Error updating reminder track for reminder %s: %v", reminderKey, err)
				}
				track, gErr := r.getReminderTrack(context.TODO(), reminderKey)
				if gErr != nil {
					log.Errorf("Error retrieving reminder %s: %v", reminderKey, gErr)
				} else {
					eTag = track.Etag
				}
			} else {
				log.Error("Could not find active reminder with key: %s", reminderKey)
				nextTimer = nil
				return
			}

			if reminder.TickExecuted() {
				nextTimer = nil
				break loop
			}

			nextTick, active = reminder.NextTick()
			if !active {
				log.Infof("Reminder %s with parameters: dueTime: %s, period: %s has expired", reminderKey, reminder.DueTime, reminder.Period)
				nextTimer = nil
				break loop
			}

			nextTimer.Reset(nextTick.Sub(r.clock.Now()))
			if err != nil {
				diag.UpdateSpanStatusFromHTTPStatus(span, http.StatusInternalServerError)
			}
			if span != nil {
				diag.UpdateSpanStatusFromHTTPStatus(span, http.StatusOK)
				span.End()
			}
		}

	delete:
		err = r.DeleteReminder(context.TODO(), internal.DeleteReminderRequest{
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

func (r *reminders) getReminderTrack(ctx context.Context, key string) (*internal.ReminderTrack, error) {
	store, err := r.stateStoreProviderFn()
	if err != nil {
		return nil, err
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(r.storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(r.storeName, resiliency.Statestore)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx, policyDef)
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
	track := &internal.ReminderTrack{
		RepetitionLeft: -1,
	}
	_ = json.Unmarshal(resp.Data, track)
	track.Etag = resp.ETag
	return track, nil
}

func (r *reminders) updateReminderTrack(ctx context.Context, key string, repetition int, lastInvokeTime time.Time, etag *string, traceState string) error {
	store, err := r.stateStoreProviderFn()
	if err != nil {
		return err
	}

	track := internal.ReminderTrack{
		LastFiredTime:  lastInvokeTime,
		RepetitionLeft: repetition,
		TraceState:     traceState,
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(r.storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(r.storeName, resiliency.Statestore)
	} else {
		// Else, we can rely on the underlying operations all being covered by resiliency.
		noOp := resiliency.NoOp{}
		policyDef = noOp.EndpointPolicy("", "")
	}
	policyRunner := resiliency.NewRunner[any](ctx, policyDef)
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
