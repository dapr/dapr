/*
Copyright 2024 The Dapr Authors
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
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

const (
	inboxKeyPrefix       = "inbox"
	historyKeyPrefix     = "history"
	customStatusKey      = "customStatus"
	metadataKey          = "metadata"
	propagatedHistoryKey = "propagated-history"
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
	CustomStatus *wrapperspb.StringValue
	Generation   uint64

	// IncomingHistory is the propagated history this workflow received from
	// its caller. Stored as a separate state key, NOT as part of history
	// events. Set once at workflow creation, never modified.
	IncomingHistory *protos.PropagatedHistory

	// change tracking
	inboxAddedCount        int
	inboxRemovedCount      int
	historyAddedCount      int
	historyRemovedCount    int
	incomingHistoryChanged bool
}

// TODO: @joshvanl: remove in v1.16
type legacyWorkflowStateMetadata struct {
	InboxLength   uint64
	HistoryLength uint64
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
	s.CustomStatus = nil
	s.Generation++
}

// ResetChangeTracking resets the change tracking counters. This should be called after a save request.
func (s *State) ResetChangeTracking() {
	s.inboxAddedCount = 0
	s.inboxRemovedCount = 0
	s.historyAddedCount = 0
	s.historyRemovedCount = 0
	s.incomingHistoryChanged = false
}

// SetIncomingHistory sets the received propagated history on the state.
func (s *State) SetIncomingHistory(ph *protos.PropagatedHistory) {
	s.IncomingHistory = ph
	s.incomingHistoryChanged = true
}

func (s *State) ApplyRuntimeStateChanges(rs *backend.WorkflowRuntimeState) {
	if rs.GetContinuedAsNew() {
		s.historyRemovedCount += len(s.History)
		s.historyAddedCount = 0
		s.History = nil
	}

	newHistoryEvents := rs.GetNewEvents()
	s.History = append(s.History, newHistoryEvents...)
	s.historyAddedCount += len(newHistoryEvents)

	s.CustomStatus = rs.GetCustomStatus()
}

func (s *State) AddToInbox(e *backend.HistoryEvent) {
	s.Inbox = append(s.Inbox, e)
	s.inboxAddedCount++
}

func (s *State) AddToHistory(e *backend.HistoryEvent) {
	s.History = append(s.History, e)
	s.historyAddedCount++
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

func (s *State) GetSaveRequest(actorID string) (*api.TransactionalRequest, error) {
	// TODO: Batching up the save requests into smaller chunks to avoid batch size limits in Dapr state stores.
	req := &api.TransactionalRequest{
		ActorType: s.workflowActorType,
		ActorID:   actorID,
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
		cs := s.CustomStatus
		if cs == nil {
			cs = &wrapperspb.StringValue{}
		}

		csProto, err := proto.Marshal(cs)
		if err != nil {
			return nil, err
		}

		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			Request:   api.TransactionalUpsert{Key: customStatusKey, Value: csProto},
		})
	}

	if s.incomingHistoryChanged && s.IncomingHistory != nil {
		phBytes, err := proto.Marshal(s.IncomingHistory)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal incoming propagated history: %w", err)
		}
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			Request:   api.TransactionalUpsert{Key: propagatedHistoryKey, Value: phBytes},
		})
	}

	metaProto, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		InboxLength:   uint64(len(s.Inbox)),
		HistoryLength: uint64(len(s.History)),
		Generation:    s.Generation,
	})
	if err != nil {
		return nil, err
	}

	// Every time we save, we also update the metadata with information about the size of the history and inbox,
	// as well as the generation of the workflow.
	req.Operations = append(req.Operations, api.TransactionalOperation{
		Operation: api.Upsert,
		Request:   api.TransactionalUpsert{Key: metadataKey, Value: metaProto},
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

	return fmt.Sprintf("Inbox:%s\nHistory:%s\nCustomStatus:%v\nGeneration:%d\ninboxAddedCount:%d\ninboxRemovedCount:%d\nhistoryAddedCount:%d\nhistoryRemovedCount:%d\nconfig:%s",
		strings.Join(inbox, ", "), strings.Join(history, ", "),
		s.CustomStatus, s.Generation,
		s.inboxAddedCount, s.inboxRemovedCount,
		s.historyAddedCount, s.historyRemovedCount,
		fmt.Sprintf("AppID='%s' workflowActorType='%s' activityActorType='%s'", s.appID, s.workflowActorType, s.activityActorType),
	)
}

func addStateOperations(req *api.TransactionalRequest, keyPrefix string, events []*backend.HistoryEvent, addedCount int, removedCount int) error {
	// TODO: Investigate whether Dapr state stores put limits on batch sizes. It seems some storage
	//       providers have limits and we need to know if that impacts this algorithm:
	//       https://learn.microsoft.com/azure/cosmos-db/nosql/transactional-batch#limitations
	for i := len(events) - addedCount; i < len(events); i++ {
		data, err := proto.Marshal(events[i])
		if err != nil {
			return err
		}

		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			//nolint:gosec
			Request: api.TransactionalUpsert{Key: getMultiEntryKeyName(keyPrefix, uint64(i)), Value: data},
		})
	}

	for i := len(events); i < removedCount; i++ {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,

			Request: api.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, uint64(i))},
		})
	}

	return nil
}

func addPurgeStateOperations(req *api.TransactionalRequest, keyPrefix string, events []*backend.HistoryEvent) error {
	// TODO: Investigate whether Dapr state stores put limits on batch sizes. It seems some storage
	//       providers have limits and we need to know if that impacts this algorithm:
	//       https://learn.microsoft.com/azure/cosmos-db/nosql/transactional-batch#limitations
	for i := range events {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,

			Request: api.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, uint64(i))},
		})
	}

	return nil
}

func LoadWorkflowState(ctx context.Context, state state.Interface, actorID string, opts Options) (*State, error) {
	loadStartTime := time.Now()

	// Load metadata
	req := api.GetStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		Key:       metadataKey,
	}

	res, err := state.Get(ctx, &req, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow metadata: %w", err)
	}

	if len(res.Data) == 0 {
		// no state found
		return nil, nil
	}

	var metadata backend.BackendWorkflowStateMetadata
	if err = proto.Unmarshal(res.Data, &metadata); err != nil {
		// TODO: @joshvanl: remove in v1.16
		var metadataJSON legacyWorkflowStateMetadata
		jerr := json.Unmarshal(res.Data, &metadataJSON)
		if jerr != nil {
			return nil, fmt.Errorf("failed to unmarshal workflow metadata: %w", err)
		}

		wfLogger.Debugf("Loaded legacy workflow state metadata: %s", res.Data)

		metadata.Generation = metadataJSON.Generation
		metadata.InboxLength = metadataJSON.InboxLength
		metadata.HistoryLength = metadataJSON.HistoryLength
	}

	// Load inbox, history, and custom status using a bulk request
	wState := NewState(opts)
	wState.Generation = metadata.GetGeneration()
	wState.Inbox = make([]*backend.HistoryEvent, 0, metadata.GetInboxLength())
	wState.History = make([]*backend.HistoryEvent, 0, metadata.GetHistoryLength())

	bulkReq := &api.GetBulkStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		// Initializing with size for all the inbox, history, custom status, and propagated history
		Keys: make([]string, metadata.GetInboxLength()+metadata.GetHistoryLength()+2),
	}

	var n int

	bulkReq.Keys[n] = customStatusKey
	n++
	bulkReq.Keys[n] = propagatedHistoryKey
	n++

	for i := range metadata.GetInboxLength() {
		bulkReq.Keys[n] = getMultiEntryKeyName(inboxKeyPrefix, i)
		n++
	}

	for i := range metadata.GetHistoryLength() {
		bulkReq.Keys[n] = getMultiEntryKeyName(historyKeyPrefix, i)
		n++
	}

	// Perform the request
	bulkRes, err := state.GetBulk(ctx, bulkReq, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow state: %w", err)
	}

	defer func() {
		// TODO: @joshvanl: remove in v1.16 where we will no longer have legacy
		// state parsing issues.
		if rerr := recover(); rerr != nil {
			wfLogger.Warnf("Found legacy workflow state, ignoring and overwriting with new storage API: %s; %v", actorID, rerr)
		}
	}()

	// Parse responses
	var key string
	for i := range metadata.GetInboxLength() {
		key = getMultiEntryKeyName(inboxKeyPrefix, i)
		if bulkRes[key] == nil {
			wfLogger.Warnf("Failed to load inbox state key '%s': not found", key)
			continue
		}

		var hist backend.HistoryEvent
		err = proto.Unmarshal(bulkRes[key], &hist)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from inbox state key '%s': %w", key, err)
		}

		wState.Inbox = append(wState.Inbox, &hist)
	}

	for i := range metadata.GetHistoryLength() {
		key = getMultiEntryKeyName(historyKeyPrefix, i)
		if bulkRes[key] == nil {
			wfLogger.Warnf("Failed to load history state key '%s': not found", key)
			continue
		}

		var hist backend.HistoryEvent
		err = proto.Unmarshal(bulkRes[key], &hist)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from history state key '%s': %w", key, err)
		}

		wState.History = append(wState.History, &hist)
	}

	if len(bulkRes[customStatusKey]) > 0 {
		wState.CustomStatus = &wrapperspb.StringValue{}

		err = proto.Unmarshal(bulkRes[customStatusKey], wState.CustomStatus)
		if err != nil {
			// Fallback to JSON unmarshaling
			var customStatusValue string
			// TODO: @famarting: remove in v1.16
			jerr := json.Unmarshal(bulkRes[customStatusKey], &customStatusValue)
			if jerr != nil {
				return nil, fmt.Errorf("failed to unmarshal custom status key entry: %w", err)
			}

			wState.CustomStatus.Value = customStatusValue
		}
	}

	if len(bulkRes[propagatedHistoryKey]) > 0 {
		var ph protos.PropagatedHistory
		if err = proto.Unmarshal(bulkRes[propagatedHistoryKey], &ph); err != nil {
			wfLogger.Warnf("Failed to unmarshal propagated history for '%s': %v", actorID, err)
		} else {
			wState.IncomingHistory = &ph
		}
	}

	wfLogger.Debugf("%s: loaded %d state records in %v", actorID, 1+len(bulkRes), time.Since(loadStartTime))

	return wState, nil
}

func (s *State) GetPurgeRequest(actorID string) (*api.TransactionalRequest, error) {
	req := &api.TransactionalRequest{
		ActorType: s.workflowActorType,
		ActorID:   actorID,
		// Initial capacity should be enough to contain the entire inbox, history, and custom status + metadata
		Operations: make([]api.TransactionalOperation, 0, len(s.Inbox)+len(s.History)+2),
	}

	// Inbox Purging
	err := addPurgeStateOperations(req, inboxKeyPrefix, s.Inbox)
	if err != nil {
		return nil, err
	}

	// History Purging
	err = addPurgeStateOperations(req, historyKeyPrefix, s.History)
	if err != nil {
		return nil, err
	}

	req.Operations = append(req.Operations,
		api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: customStatusKey},
		},
		api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: metadataKey},
		},
		api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: propagatedHistoryKey},
		},
	)

	return req, nil
}

func (s *State) ToWorkflowState() *protos.BackendWorkflowState {
	return &protos.BackendWorkflowState{
		Inbox:        s.Inbox,
		History:      s.History,
		CustomStatus: s.CustomStatus,
		Generation:   s.Generation,
	}
}

func (s *State) FromWorkflowState(state *protos.BackendWorkflowState) {
	s.Reset()

	for _, e := range state.GetInbox() {
		s.AddToInbox(e)
	}

	for _, e := range state.GetHistory() {
		s.AddToHistory(e)
	}

	s.CustomStatus = state.GetCustomStatus()
	s.Generation = state.GetGeneration()
}

func getMultiEntryKeyName(prefix string, i uint64) string {
	return fmt.Sprintf("%s-%06d", prefix, i)
}
