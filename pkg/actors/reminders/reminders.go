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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/utils/clock"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/internal"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"
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
	apiLevel             *atomic.Uint32
	runningCh            chan struct{}
	executeReminderFn    internal.ExecuteReminderFn
	remindersLock        sync.RWMutex
	reminders            map[string][]ActorReminderReference
	activeReminders      *sync.Map
	evaluationChan       chan struct{}
	evaluationQueue      chan struct{}
	stateStoreProviderFn internal.StateStoreProviderFn
	resiliency           resiliency.Provider
	config               internal.Config
	lookUpActorFn        internal.LookupActorFn
	metricsCollector     remindersMetricsCollectorFn
}

// NewRemindersProviderOpts contains the options for the NewRemindersProvider function.
type NewRemindersProviderOpts struct {
	StoreName string
	Config    internal.Config
	APILevel  *atomic.Uint32
}

// NewRemindersProvider returns a reminders provider.
func NewRemindersProvider(opts internal.ActorsProviderOptions) internal.RemindersProvider {
	return &reminders{
		clock:            opts.Clock,
		apiLevel:         opts.APILevel,
		runningCh:        make(chan struct{}),
		reminders:        map[string][]ActorReminderReference{},
		activeReminders:  &sync.Map{},
		evaluationChan:   make(chan struct{}, 1),
		evaluationQueue:  make(chan struct{}, 1),
		config:           opts.Config,
		resiliency:       opts.Resiliency,
		metricsCollector: diag.DefaultMonitoring.ActorReminders,
	}
}

func (r *reminders) SetExecuteReminderFn(fn internal.ExecuteReminderFn) {
	r.executeReminderFn = fn
}

func (r *reminders) SetStateStoreProviderFn(fn internal.StateStoreProviderFn) {
	r.stateStoreProviderFn = fn
}

func (r *reminders) SetLookupActorFn(fn internal.LookupActorFn) {
	r.lookUpActorFn = fn
}

func (r *reminders) SetMetricsCollectorFn(fn remindersMetricsCollectorFn) {
	r.metricsCollector = fn
}

// OnPlacementTablesUpdated is invoked when the actors runtime received an updated placement tables.
func (r *reminders) OnPlacementTablesUpdated(ctx context.Context) {
	go func() {
		// To handle bursts, use a queue so no more than one evaluation can be queued up at the same time, since they'd all fetch the same data anyways
		select {
		case r.evaluationQueue <- struct{}{}:
			// Queue isn't full
		default:
			// There's already one invocation in the queue so no need to queue up another one
			return
		}

		// r.evaluationQueue is released in the handler after obtaining the evaluationChan lock
		r.evaluateReminders(ctx)
	}()
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
	storeName, store, err := r.stateStoreProviderFn()
	if err != nil {
		return err
	}

	// Wait for the evaluation chan lock
	if !r.waitForEvaluationChan() {
		return errors.New("error creating reminder: timed out after 30s")
	}
	defer func() {
		// Release the evaluation chan lock
		<-r.evaluationChan
	}()

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

	config := retry.DefaultConfig()
	config.Multiplier = 1.0
	b := config.NewBackOffWithContext(ctx)

	err = retry.NotifyRecover(
		func() error {
			innerErr := r.storeReminder(ctx, storeName, store, reminder, stop)
			if innerErr != nil {
				// If the etag is mismatched, we can retry the operation.
				if isEtagMismatchError(innerErr) {
					return innerErr
				}

				log.Errorf("Error storing reminder: %v", innerErr)
				return backoff.Permanent(innerErr)
			}
			return nil
		},
		b,
		func(err error, d time.Duration) {
			log.Debugf("Attempting to store reminder again after error: %v", err)
		},
		func() {
			log.Debug("Storing of reminder successful")
		},
	)
	if err != nil {
		return err
	}

	// Start the reminder
	return r.startReminder(reminder, stop)
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
		return errors.New("error deleting reminder: timed out after 30s")
	}
	defer func() {
		// Release the evaluation chan lock
		<-r.evaluationChan
	}()

	config := retry.DefaultConfig()
	config.Multiplier = 1.0
	b := config.NewBackOffWithContext(ctx)

	err := retry.NotifyRecover(
		func() error {
			innerErr := r.doDeleteReminder(ctx, req.ActorType, req.ActorID, req.Name)
			if innerErr != nil {
				// If the etag is mismatched, we can retry the operation.
				if isEtagMismatchError(innerErr) {
					return innerErr
				}

				log.Errorf("Error deleting reminder: %v", innerErr)
				return backoff.Permanent(innerErr)
			}
			return nil
		},
		b,
		func(err error, d time.Duration) {
			log.Debugf("Attempting to delete reminder again after error: %v", err)
		},
		func() {
			log.Debug("Deletion of reminder successful")
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (r *reminders) evaluateReminders(ctx context.Context) {
	// Wait for the evaluation channel
	select {
	case r.evaluationChan <- struct{}{}:
		// All good, continue
	case <-r.runningCh:
		// Processor is shutting down
		<-r.evaluationQueue
		return
	}
	defer func() {
		// Release the evaluation chan lock
		<-r.evaluationChan
	}()

	// Allow another evaluation operation to get queued up
	<-r.evaluationQueue

	if r.config.HostedActorTypes == nil {
		log.Info("hostedActorTypes is nil, skipping reminder evaluation")
		return
	}

	var wg sync.WaitGroup
	ats := r.config.HostedActorTypes.ListActorTypes()
	for _, t := range ats {
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
				isLocalActor, targetActorAddress := r.lookUpActorFn(ctx, rmd.ActorType, rmd.ActorID)
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
}

func (r *reminders) waitForEvaluationChan() bool {
	t := r.clock.NewTimer(30 * time.Second)

	select {
	// Evaluation channel was not freed up in time
	case <-t.C():
		return false

	// The provider is shutting down
	case <-r.runningCh:
		if !t.Stop() {
			<-t.C()
		}
		return false

	// Evaluation chan is available
	case r.evaluationChan <- struct{}{}:
		if !t.Stop() {
			<-t.C()
		}
		return true
	}
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
	storeName, store, err := r.stateStoreProviderFn()
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
	if r.resiliency != nil && !r.resiliency.PolicyDefined(storeName, resiliency.ComponentOutboundPolicy) {
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
		partitionOp, rErr := r.saveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata)
		if rErr != nil {
			return false, fmt.Errorf("failed to create request for storing reminders: %w", rErr)
		}
		stateOperations := []state.TransactionalStateOperation{
			partitionOp,
			r.saveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata),
		}
		rErr = r.executeStateStoreTransaction(ctx, storeName, store, stateOperations, stateMetadata)
		if rErr != nil {
			return false, fmt.Errorf("error saving reminders partition and metadata: %w", rErr)
		}

		if r.metricsCollector != nil {
			r.metricsCollector(actorType, int64(len(reminders)))
		}
		r.remindersLock.Lock()
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

	if r.resiliency != nil && !r.resiliency.PolicyDefined(storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore)
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

func (r *reminders) storeReminder(ctx context.Context, storeName string, store internal.TransactionalStateStore, reminder *internal.Reminder, stopChannel chan struct{}) error {
	// Store the reminder in active reminders list
	reminderKey := reminder.Key()

	stored, loaded := r.activeReminders.LoadOrStore(reminderKey, stopChannel)
	if loaded {
		// If the value was loaded, we have a race condition: another goroutine is trying to store the same reminder
		return fmt.Errorf("failed to store reminder %s: reminder was created concurrently by another goroutine", reminderKey)
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(storeName, resiliency.ComponentOutboundPolicy) {
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
		partitionOp, err := r.saveRemindersInPartitionRequest(stateKey, remindersInPartition, etag, stateMetadata)
		if err != nil {
			return struct{}{}, fmt.Errorf("failed to create request for storing reminders: %w", err)
		}
		stateOperations := []state.TransactionalStateOperation{
			partitionOp,
			r.saveActorTypeMetadataRequest(reminder.ActorType, actorMetadata, stateMetadata),
		}
		rErr = r.executeStateStoreTransaction(ctx, storeName, store, stateOperations, stateMetadata)
		if rErr != nil {
			return struct{}{}, fmt.Errorf("error saving reminders partition and metadata: %w", rErr)
		}

		if r.metricsCollector != nil {
			r.metricsCollector(reminder.ActorType, int64(len(reminders)))
		}
		r.remindersLock.Lock()
		r.reminders[reminder.ActorType] = reminders
		r.remindersLock.Unlock()
		return struct{}{}, nil
	})
	if err != nil {
		// Remove the value from the in-memory cache
		r.activeReminders.CompareAndDelete(reminderKey, stored)
		return err
	}
	return nil
}

func (r *reminders) executeStateStoreTransaction(ctx context.Context, storeName string, store internal.TransactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error {
	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore)
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

func (r *reminders) serializeRemindersToProto(reminders []internal.Reminder) ([]byte, error) {
	pb := &internalv1pb.Reminders{
		Reminders: make([]*internalv1pb.Reminder, len(reminders)),
	}
	for i, rm := range reminders {
		pb.Reminders[i] = &internalv1pb.Reminder{
			ActorId:   rm.ActorID,
			ActorType: rm.ActorType,
			Name:      rm.Name,
			Period:    rm.Period.String(),
			DueTime:   rm.DueTime,
		}
		if !rm.RegisteredTime.IsZero() {
			pb.Reminders[i].RegisteredTime = timestamppb.New(rm.RegisteredTime)
		}
		if !rm.ExpirationTime.IsZero() {
			pb.Reminders[i].ExpirationTime = timestamppb.New(rm.ExpirationTime)
		}
		if len(rm.Data) > 0 && !bytes.Equal(rm.Data, []byte("null")) {
			pb.Reminders[i].Data = rm.Data
		}
	}
	res, err := proto.Marshal(pb)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize reminders as protobuf: %w", err)
	}

	// Prepend the prefix "\0pb" to indicate this is protobuf
	res = append([]byte{0, 'p', 'b'}, res...)

	return res, nil
}

func (r *reminders) unserialize(data []byte) ([]internal.Reminder, error) {
	// Check if we have the protobuf prefix
	if bytes.HasPrefix(data, []byte{0, 'p', 'b'}) {
		return r.unserializeRemindersFromProto(data[3:])
	}

	// Fallback to unserializing from JSON
	var batch []internal.Reminder
	err := json.Unmarshal(data, &batch)
	return batch, err
}

//nolint:protogetter
func (r *reminders) unserializeRemindersFromProto(data []byte) ([]internal.Reminder, error) {
	pb := internalv1pb.Reminders{}
	err := proto.Unmarshal(data, &pb)
	if err != nil {
		return nil, fmt.Errorf("failed to unserialize reminders from protobuf: %w", err)
	}

	res := make([]internal.Reminder, len(pb.GetReminders()))
	for i, rm := range pb.GetReminders() {
		if rm == nil {
			return nil, errors.New("unserialized reminder object is nil")
		}
		res[i] = internal.Reminder{
			ActorID:   rm.ActorId,
			ActorType: rm.ActorType,
			Name:      rm.Name,
			DueTime:   rm.DueTime,
			Period:    internal.NewEmptyReminderPeriod(),
		}

		if len(rm.Data) > 0 {
			res[i].Data = rm.Data
		}
		if rm.Period != "" {
			err = res[i].Period.UnmarshalJSON([]byte(rm.Period))
			if err != nil {
				return nil, fmt.Errorf("failed to unserialize reminder period: %w", err)
			}
		}

		expirationTimePb := rm.GetExpirationTime()
		if expirationTimePb != nil && expirationTimePb.IsValid() {
			expirationTime := expirationTimePb.AsTime()
			if !expirationTime.IsZero() {
				res[i].ExpirationTime = expirationTime
			}
		}

		registeredTimePb := rm.GetRegisteredTime()
		if registeredTimePb != nil && registeredTimePb.IsValid() {
			registeredTime := registeredTimePb.AsTime()
			if !registeredTime.IsZero() {
				res[i].RegisteredTime = registeredTime
			}
		}
	}

	return res, nil
}

func (r *reminders) saveRemindersInPartitionRequest(stateKey string, reminders []internal.Reminder, etag *string, metadata map[string]string) (state.SetRequest, error) {
	req := state.SetRequest{
		Key:      stateKey,
		ETag:     etag,
		Metadata: metadata,
		Options: state.SetStateOption{
			Concurrency: state.FirstWrite,
		},
	}

	// If APILevelFeatureRemindersProtobuf is enabled, then serialize as protobuf which is more efficient
	// Otherwise, fall back to sending the data as-is in the request (which will serialize it as JSON)
	if internal.APILevelFeatureRemindersProtobuf.IsEnabled(r.apiLevel.Load()) {
		var err error
		req.Value, err = r.serializeRemindersToProto(reminders)
		if err != nil {
			return req, err
		}
	} else {
		req.Value = reminders
	}

	return req, nil
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
	storeName, store, err := r.stateStoreProviderFn()
	if err != nil {
		return nil, nil, err
	}

	actorMetadata, err := r.getActorTypeMetadata(ctx, actorType, migrate)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read actor type metadata: %w", err)
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && r.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore) != nil {
		policyDef = r.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore)
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

			// Data can be empty if there's no reminder, when serialized as protobuf
			var batch []internal.Reminder
			if len(resp.Data) == 0 {
				return nil, nil, fmt.Errorf("no data found for reminder partition %v: %w", resp.Key, err)
			}

			batch, err = r.unserialize(resp.Data)
			if err != nil {
				return nil, nil, fmt.Errorf("could not parse actor reminders partition %v: %w", resp.Key, err)
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
		reminders, err = r.unserialize(resp.Data)
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

// getActorMetadata gets the metadata object for the given actor type.
// If "migrate" is true, it also performs migration of reminders if needed. Note that this should be set to "true" only by a caller who owns a lock via evaluationChan.
func (r *reminders) getActorTypeMetadata(ctx context.Context, actorType string, migrate bool) (*ActorMetadata, error) {
	storeName, store, err := r.stateStoreProviderFn()
	if err != nil {
		return nil, err
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && r.resiliency.PolicyDefined(storeName, resiliency.ComponentOutboundPolicy) {
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
			rErr = r.migrateRemindersForActorType(ctx, storeName, store, actorType, actorMetadata)
			if rErr != nil {
				return nil, rErr
			}
		}

		return actorMetadata, nil
	})
}

// migrateRemindersForActorType migrates reminders for actors of a given type.
// Note that this method should be invoked by a caller that owns the evaluationChan lock.
func (r *reminders) migrateRemindersForActorType(ctx context.Context, storeName string, store internal.TransactionalStateStore, actorType string, actorMetadata *ActorMetadata) error {
	reminderPartitionCount := r.config.GetRemindersPartitionCountForType(actorType)
	if actorMetadata.RemindersMetadata.PartitionCount == reminderPartitionCount {
		return nil
	}

	if actorMetadata.RemindersMetadata.PartitionCount > reminderPartitionCount {
		log.Errorf("Cannot decrease number of partitions for reminders of actor type %s", actorType)
		return nil
	}

	log.Warnf("Migrating actor metadata record for actor type %s", actorType)

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
		stateOperations[i], err = r.saveRemindersInPartitionRequest(stateKey, actorRemindersPartitions[i], nil, stateMetadata)
		if err != nil {
			return fmt.Errorf("failed to create request for reminders in partition %d: %w", i, err)
		}
	}

	// Also create a request to save the new metadata, so the new "metadataID" becomes the new de facto referenced list for reminders
	stateOperations[len(stateOperations)-1] = r.saveActorTypeMetadataRequest(actorType, actorMetadata, stateMetadata)

	// Perform all operations in a transaction
	err = r.executeStateStoreTransaction(ctx, storeName, store, stateOperations, stateMetadata)
	if err != nil {
		return fmt.Errorf("failed to perform transaction to migrate records for actor type %s: %w", actorType, err)
	}

	log.Warnf("Completed actor metadata record migration for actor type %s, new metadata ID = %s", actorType, actorMetadata.ID)
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
				err = r.updateReminderTrack(context.TODO(), reminderKey, reminder.RepeatsLeft(), nextTick, eTag)
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
	storeName, store, err := r.stateStoreProviderFn()
	if err != nil {
		return nil, err
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore)
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

func (r *reminders) updateReminderTrack(ctx context.Context, key string, repetition int, lastInvokeTime time.Time, etag *string) error {
	storeName, store, err := r.stateStoreProviderFn()
	if err != nil {
		return err
	}

	track := internal.ReminderTrack{
		LastFiredTime:  lastInvokeTime,
		RepetitionLeft: repetition,
	}

	var policyDef *resiliency.PolicyDefinition
	if r.resiliency != nil && !r.resiliency.PolicyDefined(storeName, resiliency.ComponentOutboundPolicy) {
		policyDef = r.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore)
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

func isEtagMismatchError(err error) bool {
	if err == nil {
		return false
	}
	var etagErr *state.ETagError
	if errors.As(err, &etagErr) {
		return etagErr.Kind() == state.ETagMismatch
	}
	return false
}
