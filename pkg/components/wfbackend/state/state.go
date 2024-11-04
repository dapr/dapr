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

package state

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/kit/logger"
)

const (
	inboxKeyPrefix   = "inbox"
	historyKeyPrefix = "history"
	customStatusKey  = "customStatus"
	metadataKey      = "metadata"
)

var wfLogger = logger.NewLogger("dapr.runtime.actor.target.workflow.state")

type Options struct {
	AppID             string
	WorkflowActorType string
	ActivityActorType string
}

type State struct {
	appID             string
	workflowActorType string
	activityActorType string

	Inbox        []*backend.HistoryEvent
	History      []*backend.HistoryEvent
	CustomStatus string
	Generation   uint64

	// change tracking
	inboxAddedCount     int
	inboxRemovedCount   int
	historyAddedCount   int
	historyRemovedCount int
}

type serializableWorkflowState struct {
	Inbox        [][]byte
	History      [][]byte
	CustomStatus string
	Generation   uint64
}

type workflowStateMetadata struct {
	InboxLength   int
	HistoryLength int
	Generation    uint64
}

func NewState(opts Options) *State {
	return &State{
		Generation:        1,
		appID:             opts.AppID,
		workflowActorType: opts.WorkflowActorType,
		activityActorType: opts.ActivityActorType,
	}
}

func (s *State) Reset() {
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
func (s *State) ResetChangeTracking() {
	s.inboxAddedCount = 0
	s.inboxRemovedCount = 0
	s.historyAddedCount = 0
	s.historyRemovedCount = 0
}

func (s *State) ApplyRuntimeStateChanges(runtimeState *backend.OrchestrationRuntimeState) {
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

func (s *State) AddToInbox(e *backend.HistoryEvent) {
	s.Inbox = append(s.Inbox, e)
	s.inboxAddedCount++
}

func (s *State) ClearInbox() {
	for _, e := range s.Inbox {
		if e.GetTimerFired() != nil {
			// ignore timer events since those aren't saved into the state store
			continue
		}
		s.inboxRemovedCount++
	}
	s.Inbox = nil
	s.inboxAddedCount = 0
}

func (s *State) GetSaveRequest(actorID string) (*requestresponse.TransactionalRequest, error) {
	// TODO: Batching up the save requests into smaller chunks to avoid batch size limits in Dapr state stores.
	req := &requestresponse.TransactionalRequest{
		ActorType:  s.workflowActorType,
		ActorID:    actorID,
		Operations: make([]requestresponse.TransactionalOperation, 0, 100),
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
		req.Operations = append(req.Operations, requestresponse.TransactionalOperation{
			Operation: requestresponse.Upsert,
			Request:   requestresponse.TransactionalUpsert{Key: customStatusKey, Value: s.CustomStatus},
		})
	}

	// Every time we save, we also update the metadata with information about the size of the history and inbox,
	// as well as the generation of the workflow.
	metadata := workflowStateMetadata{
		InboxLength:   len(s.Inbox),
		HistoryLength: len(s.History),
		Generation:    s.Generation,
	}
	req.Operations = append(req.Operations, requestresponse.TransactionalOperation{
		Operation: requestresponse.Upsert,
		Request:   requestresponse.TransactionalUpsert{Key: metadataKey, Value: metadata},
	})

	return req, nil
}

// String implements fmt.Stringer and is primarily used for debugging purposes.
func (s *State) String() string {
	if s == nil {
		return "(nil)"
	}

	inbox := make([]string, len(s.Inbox))
	for i, v := range s.Inbox {
		if v == nil {
			inbox[i] = "[(nil)]"
		} else {
			inbox[i] = "[" + v.String() + "]"
		}
	}
	history := make([]string, len(s.History))
	for i, v := range s.History {
		if v == nil {
			history[i] = "[(nil)]"
		} else {
			history[i] = "[" + v.String() + "]"
		}
	}
	return fmt.Sprintf("Inbox:%s\nHistory:%s\nCustomStatus:%s\nGeneration:%d\ninboxAddedCount:%d\ninboxRemovedCount:%d\nhistoryAddedCount:%d\nhistoryRemovedCount:%d\nconfig:%s",
		strings.Join(inbox, ", "), strings.Join(history, ", "),
		s.CustomStatus, s.Generation,
		s.inboxAddedCount, s.inboxRemovedCount,
		s.historyAddedCount, s.historyRemovedCount,
		fmt.Sprintf("AppID='%s' workflowActorType='%s' activityActorType='%s'", s.appID, s.workflowActorType, s.activityActorType),
	)
}

// EncodeWorkflowState encodes the workflow state into a byte array.
// It only encodes the inbox, history, and custom status.
func (s *State) EncodeWorkflowState() ([]byte, error) {
	// Encode history events
	encodedHistory := make([][]byte, len(s.History))
	for i, event := range s.History {
		encodedEvent, err := backend.MarshalHistoryEvent(event)
		if err != nil {
			return nil, err
		}
		encodedHistory[i] = encodedEvent
	}
	encodedInbox := make([][]byte, len(s.Inbox))
	for i, event := range s.Inbox {
		encodedEvent, err := backend.MarshalHistoryEvent(event)
		if err != nil {
			return nil, err
		}
		encodedInbox[i] = encodedEvent
	}

	// Encode workflowState
	var resBuffer bytes.Buffer
	if err := gob.NewEncoder(&resBuffer).Encode(&serializableWorkflowState{
		Inbox:        encodedInbox,
		History:      encodedHistory,
		CustomStatus: s.CustomStatus,
		Generation:   s.Generation,
	}); err != nil {
		return nil, err
	}
	return resBuffer.Bytes(), nil
}

// DecodeWorkflowState decodes the workflow state from a byte array encoded using `EncodeWorkflowState`.
// It only decodes the inbox, history, and custom status.
func (s *State) DecodeWorkflowState(encodedState []byte) error {
	// Decode workflowState
	var decodedState serializableWorkflowState

	if err := gob.NewDecoder(bytes.NewReader(encodedState)).Decode(&decodedState); err != nil {
		return err
	}

	// Decode history events
	s.History = make([]*backend.HistoryEvent, len(decodedState.History))
	for i, encodedEvent := range decodedState.History {
		event, err := backend.UnmarshalHistoryEvent(encodedEvent)
		if err != nil {
			return err
		}
		s.History[i] = event
	}
	s.Inbox = make([]*backend.HistoryEvent, len(decodedState.Inbox))
	for i, encodedEvent := range decodedState.Inbox {
		event, err := backend.UnmarshalHistoryEvent(encodedEvent)
		if err != nil {
			return err
		}
		s.Inbox[i] = event
	}
	s.CustomStatus = decodedState.CustomStatus
	s.Generation = decodedState.Generation
	return nil
}

func addStateOperations(req *requestresponse.TransactionalRequest, keyPrefix string, events []*backend.HistoryEvent, addedCount int, removedCount int) error {
	// TODO: Investigate whether Dapr state stores put limits on batch sizes. It seems some storage
	//       providers have limits and we need to know if that impacts this algorithm:
	//       https://learn.microsoft.com/azure/cosmos-db/nosql/transactional-batch#limitations
	for i := len(events) - addedCount; i < len(events); i++ {
		e := events[i]
		data, err := backend.MarshalHistoryEvent(e)
		if err != nil {
			return err
		}
		req.Operations = append(req.Operations, requestresponse.TransactionalOperation{
			Operation: requestresponse.Upsert,
			Request:   requestresponse.TransactionalUpsert{Key: getMultiEntryKeyName(keyPrefix, i), Value: data},
		})
	}
	for i := len(events); i < removedCount; i++ {
		req.Operations = append(req.Operations, requestresponse.TransactionalOperation{
			Operation: requestresponse.Delete,
			Request:   requestresponse.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, i)},
		})
	}
	return nil
}

func addPurgeStateOperations(req *requestresponse.TransactionalRequest, keyPrefix string, events []*backend.HistoryEvent) error {
	// TODO: Investigate whether Dapr state stores put limits on batch sizes. It seems some storage
	//       providers have limits and we need to know if that impacts this algorithm:
	//       https://learn.microsoft.com/azure/cosmos-db/nosql/transactional-batch#limitations
	for i := range events {
		req.Operations = append(req.Operations, requestresponse.TransactionalOperation{
			Operation: requestresponse.Delete,
			Request:   requestresponse.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, i)},
		})
	}
	return nil
}

func LoadWorkflowState(ctx context.Context, state state.Interface, actorID string, opts Options) (*State, error) {
	loadStartTime := time.Now()
	loadedRecords := 0

	// Load metadata
	req := requestresponse.GetStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		Key:       metadataKey,
	}
	res, err := state.Get(ctx, &req)
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

	// Load inbox, history, and custom status using a bulk request
	wState := NewState(opts)
	wState.Generation = metadata.Generation
	wState.Inbox = make([]*backend.HistoryEvent, metadata.InboxLength)
	wState.History = make([]*backend.HistoryEvent, metadata.HistoryLength)

	bulkReq := &requestresponse.GetBulkStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		// Initializing with size for all the inbox, history, and custom status
		Keys: make([]string, metadata.InboxLength+metadata.HistoryLength+1),
	}

	var n int
	bulkReq.Keys[n] = customStatusKey
	n++
	for i := range metadata.InboxLength {
		bulkReq.Keys[n] = getMultiEntryKeyName(inboxKeyPrefix, i)
		n++
	}
	for i := range metadata.HistoryLength {
		bulkReq.Keys[n] = getMultiEntryKeyName(historyKeyPrefix, i)
		n++
	}

	// Perform the request
	bulkRes, err := state.GetBulk(ctx, bulkReq)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow state: %w", err)
	}

	// Parse responses
	loadedRecords += len(bulkRes)
	var key string
	for i := range metadata.InboxLength {
		key = getMultiEntryKeyName(inboxKeyPrefix, i)
		if bulkRes[key] == nil {
			return nil, fmt.Errorf("failed to load inbox state key '%s': not found", key)
		}
		wState.Inbox[i], err = backend.UnmarshalHistoryEvent(bulkRes[key])
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from inbox state key '%s': %w", key, err)
		}
	}
	for i := range metadata.HistoryLength {
		key = getMultiEntryKeyName(historyKeyPrefix, i)
		if bulkRes[key] == nil {
			return nil, fmt.Errorf("failed to load history state key '%s': not found", key)
		}
		wState.History[i], err = backend.UnmarshalHistoryEvent(bulkRes[key])
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from history state key '%s': %w", key, err)
		}
	}

	if len(bulkRes[customStatusKey]) > 0 {
		err = json.Unmarshal(bulkRes[customStatusKey], &wState.CustomStatus)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON from custom status key entry: %w", err)
		}
	}

	wfLogger.Infof("%s: loaded %d state records in %v", actorID, loadedRecords, time.Since(loadStartTime))
	return wState, nil
}

func (s *State) GetPurgeRequest(actorID string) (*requestresponse.TransactionalRequest, error) {
	req := &requestresponse.TransactionalRequest{
		ActorType: s.workflowActorType,
		ActorID:   actorID,
		// Initial capacity should be enough to contain the entire inbox, history, and custom status + metadata
		Operations: make([]requestresponse.TransactionalOperation, 0, len(s.Inbox)+len(s.History)+2),
	}

	// Inbox Purging
	if err := addPurgeStateOperations(req, inboxKeyPrefix, s.Inbox); err != nil {
		return nil, err
	}

	// History Purging
	if err := addPurgeStateOperations(req, historyKeyPrefix, s.History); err != nil {
		return nil, err
	}

	req.Operations = append(req.Operations,
		requestresponse.TransactionalOperation{
			Operation: requestresponse.Delete,
			Request:   requestresponse.TransactionalDelete{Key: customStatusKey},
		},
		requestresponse.TransactionalOperation{
			Operation: requestresponse.Delete,
			Request:   requestresponse.TransactionalDelete{Key: metadataKey},
		},
	)

	return req, nil
}

func getMultiEntryKeyName(prefix string, i int) string {
	return fmt.Sprintf("%s-%06d", prefix, i)
}
