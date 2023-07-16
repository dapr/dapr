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
package wfengine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
)

const (
	inboxKeyPrefix   = "inbox"
	historyKeyPrefix = "history"
	customStatusKey  = "customStatus"
	metadataKey      = "metadata"
)

type workflowState struct {
	Inbox        []*backend.HistoryEvent
	History      []*backend.HistoryEvent
	CustomStatus string
	Generation   uint64

	// change tracking
	inboxAddedCount     int
	inboxRemovedCount   int
	historyAddedCount   int
	historyRemovedCount int
	config              wfConfig
}

type workflowStateMetadata struct {
	InboxLength   int
	HistoryLength int
	Generation    uint64
}

func NewWorkflowState(config wfConfig) *workflowState {
	return &workflowState{
		Generation: 1,
		config:     config,
	}
}

func (s *workflowState) Reset() {
	s.inboxAddedCount = 0
	s.inboxRemovedCount += len(s.Inbox)
	s.Inbox = nil
	s.historyAddedCount = 0
	s.historyRemovedCount += len(s.History)
	s.History = nil
	s.CustomStatus = ""
	s.Generation++
}

// ResetChangeTracking resets the change tracking counters. This should be called after a save request.
func (s *workflowState) ResetChangeTracking() {
	s.inboxAddedCount = 0
	s.inboxRemovedCount = 0
	s.historyAddedCount = 0
	s.historyRemovedCount = 0
}

func (s *workflowState) ApplyRuntimeStateChanges(runtimeState *backend.OrchestrationRuntimeState) {
	if runtimeState.ContinuedAsNew() {
		s.historyRemovedCount += len(s.History)
		s.historyAddedCount = 0
		s.History = nil
	}

	newHistoryEvents := runtimeState.NewEvents()
	s.History = append(s.History, newHistoryEvents...)
	s.historyAddedCount += len(newHistoryEvents)

	s.CustomStatus = runtimeState.CustomStatus.GetValue()
}

func (s *workflowState) AddToInbox(e *backend.HistoryEvent) {
	s.Inbox = append(s.Inbox, e)
	s.inboxAddedCount++
}

func (s *workflowState) ClearInbox() {
	s.inboxRemovedCount += len(s.Inbox)
	s.Inbox = nil
	s.inboxAddedCount = 0
}

func (s *workflowState) GetSaveRequest(actorID string) (*actors.TransactionalRequest, error) {
	// TODO: Batching up the save requests into smaller chunks to avoid batch size limits in Dapr state stores.
	req := &actors.TransactionalRequest{
		ActorType:  s.config.workflowActorType,
		ActorID:    actorID,
		Operations: make([]actors.TransactionalOperation, 0, 100),
	}

	if err := addStateOperations(req, inboxKeyPrefix, s.Inbox, s.inboxAddedCount, s.inboxRemovedCount); err != nil {
		return nil, err
	}

	if err := addStateOperations(req, historyKeyPrefix, s.History, s.historyAddedCount, s.historyRemovedCount); err != nil {
		return nil, err
	}

	// We update the custom status only when the workflow itself has been updated, and not when
	// we're saving changes only to the workflow inbox.
	// CONSIDER: Only save custom status if it has changed. However, need a way to track this.
	if s.historyAddedCount > 0 || s.historyRemovedCount > 0 {
		req.Operations = append(req.Operations, actors.TransactionalOperation{
			Operation: actors.Upsert,
			Request:   actors.TransactionalUpsert{Key: customStatusKey, Value: s.CustomStatus},
		})
	}

	// Every time we save, we also update the metadata with information about the size of the history and inbox,
	// as well as the generation of the workflow.
	metadata := workflowStateMetadata{
		InboxLength:   len(s.Inbox),
		HistoryLength: len(s.History),
		Generation:    s.Generation,
	}
	req.Operations = append(req.Operations, actors.TransactionalOperation{
		Operation: actors.Upsert,
		Request:   actors.TransactionalUpsert{Key: metadataKey, Value: metadata},
	})

	return req, nil
}

func addStateOperations(req *actors.TransactionalRequest, keyPrefix string, events []*backend.HistoryEvent, addedCount int, removedCount int) error {
	// TODO: Investigate whether Dapr state stores put limits on batch sizes. It seems some storage
	//       providers have limits and we need to know if that impacts this algorithm:
	//       https://learn.microsoft.com/azure/cosmos-db/nosql/transactional-batch#limitations
	for i := len(events) - addedCount; i < len(events); i++ {
		e := events[i]
		data, err := backend.MarshalHistoryEvent(e)
		if err != nil {
			return err
		}
		req.Operations = append(req.Operations, actors.TransactionalOperation{
			Operation: actors.Upsert,
			Request:   actors.TransactionalUpsert{Key: getMultiEntryKeyName(keyPrefix, i), Value: data},
		})
	}
	for i := len(events); i < removedCount; i++ {
		req.Operations = append(req.Operations, actors.TransactionalOperation{
			Operation: actors.Delete,
			Request:   actors.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, i)},
		})
	}
	return nil
}

func addPurgeStateOperations(req *actors.TransactionalRequest, keyPrefix string, events []*backend.HistoryEvent) error {
	// TODO: Investigate whether Dapr state stores put limits on batch sizes. It seems some storage
	//       providers have limits and we need to know if that impacts this algorithm:
	//       https://learn.microsoft.com/azure/cosmos-db/nosql/transactional-batch#limitations
	for i := 0; i < len(events); i++ {
		req.Operations = append(req.Operations, actors.TransactionalOperation{
			Operation: actors.Delete,
			Request:   actors.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, i)},
		})
	}
	return nil
}

func LoadWorkflowState(ctx context.Context, actorRuntime actors.Actors, actorID string, config wfConfig) (*workflowState, error) {
	loadStartTime := time.Now()
	loadedRecords := 0

	req := actors.GetStateRequest{
		ActorType: config.workflowActorType,
		ActorID:   actorID,
		Key:       metadataKey,
	}
	res, err := actorRuntime.GetState(ctx, &req)
	loadedRecords++
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow metadata: %w", err)
	}
	if len(res.Data) == 0 {
		// no state found
		return nil, nil
	}
	var metadata workflowStateMetadata
	if err = json.Unmarshal(res.Data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow metadata: %w", err)
	}
	state := NewWorkflowState(config)
	state.Generation = metadata.Generation
	// CONSIDER: Do some of these loads in parallel
	for i := 0; i < metadata.InboxLength; i++ {
		req.Key = getMultiEntryKeyName(inboxKeyPrefix, i)
		res, err = actorRuntime.GetState(ctx, &req)
		loadedRecords++
		if err != nil {
			return nil, fmt.Errorf("failed to load workflow inbox state key '%s': %w", req.Key, err)
		}
		var e *backend.HistoryEvent
		e, err = backend.UnmarshalHistoryEvent(res.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from inbox state key entry: %w", err)
		}
		state.Inbox = append(state.Inbox, e)
	}
	for i := 0; i < metadata.HistoryLength; i++ {
		req.Key = getMultiEntryKeyName(historyKeyPrefix, i)
		res, err = actorRuntime.GetState(ctx, &req)
		loadedRecords++
		if err != nil {
			return nil, fmt.Errorf("failed to load workflow history state key '%s': %w", req.Key, err)
		}
		var e *backend.HistoryEvent
		e, err = backend.UnmarshalHistoryEvent(res.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from inbox state key entry: %w", err)
		}

		state.History = append(state.History, e)
	}

	req.Key = customStatusKey
	res, err = actorRuntime.GetState(ctx, &req)
	loadedRecords++
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow custom status key '%s': %w", req.Key, err)
	}
	if len(res.Data) > 0 {
		if err = json.Unmarshal(res.Data, &state.CustomStatus); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON from custom status key entry: %w", err)
		}
	}

	wfLogger.Infof("%s: loaded %d state records in %v", actorID, loadedRecords, time.Since(loadStartTime))
	return state, nil
}

func (s *workflowState) GetPurgeRequest(actorID string) (*actors.TransactionalRequest, error) {
	req := &actors.TransactionalRequest{
		ActorType:  s.config.workflowActorType,
		ActorID:    actorID,
		Operations: make([]actors.TransactionalOperation, 0, 100),
	}

	// Inbox Purging
	if err := addPurgeStateOperations(req, inboxKeyPrefix, s.Inbox); err != nil {
		return nil, err
	}

	// History Purging
	if err := addPurgeStateOperations(req, historyKeyPrefix, s.History); err != nil {
		return nil, err
	}

	req.Operations = append(req.Operations, actors.TransactionalOperation{
		Operation: actors.Delete,
		Request:   actors.TransactionalDelete{Key: customStatusKey},
	}, actors.TransactionalOperation{
		Operation: actors.Delete,
		Request:   actors.TransactionalDelete{Key: metadataKey},
	})

	return req, nil
}

func getMultiEntryKeyName(prefix string, i int) string {
	return fmt.Sprintf("%s-%06d", prefix, i)
}
