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
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

const (
	inboxKeyPrefix   = "inbox"
	historyKeyPrefix = "history"
	customStatusKey  = "customStatus"
	// MetadataKey is the state-store key holding the workflow's metadata row
	// (history/inbox lengths + generation). Exported so other packages can
	// refresh just this row to pick up the latest ETag without reloading the
	// full workflow state.
	MetadataKey = "metadata"
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

	// metadataETag is the state store's row-version token for the metadata
	// key, captured at load time and refreshed after every successful save.
	// Used as the version anchor for optimistic concurrency: every save
	// upserts the metadata row with this ETag, so a peer host that wrote
	// state under a stale snapshot causes our Multi to fail with an
	// ETagMismatch instead of silently overwriting persisted events. nil
	// means "no prior ETag known" (e.g. workflow is being created for the
	// first time, or the cache was just invalidated). Not persisted; lives
	// only in the in-memory cache.
	metadataETag *string

	// customStatusPersisted is observed at load time from the state
	// store's ETag for the customStatus key. It is the source of truth
	// for "does this optional key currently exist in the store" at purge
	// time, independent of whether the in-memory CustomStatus value is
	// populated (the runtime upserts customStatus with an empty proto on
	// every history delta, which loads as a nil CustomStatus).
	// Including a delete for a key that does not exist in a transactional
	// batch causes Azure Cosmos DB to abort the entire batch on the 404,
	// which leaves workflow state unpurged and the retention reminder
	// retrying forever.
	customStatusPersisted bool

	// change tracking
	inboxAddedCount     int
	inboxRemovedCount   int
	historyAddedCount   int
	historyRemovedCount int
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
	// A save with any history delta upserts the customStatus key (see
	// GetSaveRequest), so after a successful save it is now persisted.
	if s.historyAddedCount > 0 || s.historyRemovedCount > 0 {
		s.customStatusPersisted = true
	}

	s.inboxAddedCount = 0
	s.inboxRemovedCount = 0
	s.historyAddedCount = 0
	s.historyRemovedCount = 0
}

// MetadataETag returns the cached metadata ETag, or nil if none is known.
// Callers performing optimistic concurrency pass this through to the metadata
// TransactionalUpsert via [GetSaveRequest].
func (s *State) MetadataETag() *string {
	return s.metadataETag
}

// SetMetadataETag updates the cached metadata ETag. Called after a successful
// save to record the new row-version token returned by a follow-up
// metadata-only Get, so the next save can present it as the prior ETag for
// the optimistic-concurrency check.
func (s *State) SetMetadataETag(etag *string) {
	s.metadataETag = etag
}

func (s *State) ApplyRuntimeStateChanges(rs *backend.OrchestrationRuntimeState) {
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

	metaProto, err := proto.Marshal(&backend.WorkflowStateMetadata{
		InboxLength:   uint64(len(s.Inbox)),
		HistoryLength: uint64(len(s.History)),
		Generation:    s.Generation,
	})
	if err != nil {
		return nil, err
	}

	// Every time we save, we also update the metadata with information about the size of the history and inbox,
	// as well as the generation of the workflow. The metadata Upsert carries
	// the prior ETag so the whole transaction acts as one optimistic-
	// concurrency check: any peer host that saved underneath us since our
	// last load has bumped the row's ETag, so our Multi will fail with
	// ETagMismatch instead of silently overwriting their writes. A nil
	// metadataETag means "no prior version known" (first save of a brand-new
	// workflow, or post-invalidation reload) and falls through to a blind
	// upsert, which is the correct semantics for those cases.
	req.Operations = append(req.Operations, api.TransactionalOperation{
		Operation: api.Upsert,
		Request: api.TransactionalUpsert{
			Key:   MetadataKey,
			Value: metaProto,
			ETag:  s.metadataETag,
		},
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
			//nolint:gosec
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
			//nolint:gosec
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
		Key:       MetadataKey,
	}
	res, err := state.Get(ctx, &req, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow metadata: %w", err)
	}
	if len(res.Data) == 0 {
		// no state found
		return nil, nil
	}
	var metadata backend.WorkflowStateMetadata
	if err = proto.Unmarshal(res.Data, &metadata); err != nil {
		// TODO: @joshvanl: remove in v1.16
		var metadataJSON legacyWorkflowStateMetadata
		if jerr := json.Unmarshal(res.Data, &metadataJSON); jerr != nil {
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
	// Capture the metadata ETag for optimistic-concurrency on the next save.
	// nil here just means the store didn't return one for this row, which is
	// also fine: the next save will fall through to a blind upsert.
	wState.metadataETag = res.ETag
	wState.Inbox = make([]*backend.HistoryEvent, 0, metadata.GetInboxLength())
	wState.History = make([]*backend.HistoryEvent, 0, metadata.GetHistoryLength())

	bulkReq := &api.GetBulkStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		// Initializing with size for all the inbox, history, and custom status
		Keys: make([]string, metadata.GetInboxLength()+metadata.GetHistoryLength()+1),
	}

	var n int
	bulkReq.Keys[n] = customStatusKey
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
		if bulkRes[key].Data == nil {
			wfLogger.Warnf("Failed to load inbox state key '%s': not found", key)
			continue
		}
		var hist backend.HistoryEvent
		if err = proto.Unmarshal(bulkRes[key].Data, &hist); err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from inbox state key '%s': %w", key, err)
		}
		wState.Inbox = append(wState.Inbox, &hist)
	}
	for i := range metadata.GetHistoryLength() {
		key = getMultiEntryKeyName(historyKeyPrefix, i)
		if bulkRes[key].Data == nil {
			wfLogger.Warnf("Failed to load history state key '%s': not found", key)
			continue
		}
		var hist backend.HistoryEvent
		if err = proto.Unmarshal(bulkRes[key].Data, &hist); err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from history state key '%s': %w", key, err)
		}
		wState.History = append(wState.History, &hist)
	}

	if bulkRes[customStatusKey].ETag != nil {
		wState.customStatusPersisted = true
	}
	if len(bulkRes[customStatusKey].Data) > 0 {
		wState.CustomStatus = &wrapperspb.StringValue{}
		err = proto.Unmarshal(bulkRes[customStatusKey].Data, wState.CustomStatus)
		if err != nil {
			// Fallback to JSON unmarshaling
			var customStatusValue string
			// TODO: @famarting: remove in v1.16
			if jerr := json.Unmarshal(bulkRes[customStatusKey].Data, &customStatusValue); jerr != nil {
				return nil, fmt.Errorf("failed to unmarshal custom status key entry: %w", err)
			}
			wState.CustomStatus.Value = customStatusValue
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
	if err := addPurgeStateOperations(req, inboxKeyPrefix, s.Inbox); err != nil {
		return nil, err
	}

	// History Purging
	if err := addPurgeStateOperations(req, historyKeyPrefix, s.History); err != nil {
		return nil, err
	}

	// Only emit a delete for customStatus when we know the row exists. In
	// an Azure Cosmos DB transactional batch a 404 on any delete aborts
	// the whole batch (the etag-less delete tolerance in the state
	// component only applies outside transactions), which would leave
	// the workflow state in place and the retention reminder retrying
	// forever. customStatus is upserted whenever history changes
	// (possibly with an empty proto), so the in-memory CustomStatus
	// pointer cannot disambiguate "persisted as empty" from "never
	// persisted"; the persistence flag captured at load and save time
	// is the source of truth.
	if s.customStatusPersisted {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: customStatusKey},
		})
	}

	req.Operations = append(req.Operations, api.TransactionalOperation{
		Operation: api.Delete,
		Request:   api.TransactionalDelete{Key: MetadataKey},
	})

	return req, nil
}

func (s *State) ToWorkflowState() *backend.WorkflowState {
	return &backend.WorkflowState{
		Inbox:        s.Inbox,
		History:      s.History,
		CustomStatus: s.CustomStatus,
		Generation:   s.Generation,
	}
}

func (s *State) FromWorkflowState(state *backend.WorkflowState) {
	s.Reset()
	for _, e := range state.GetInbox() {
		s.AddToInbox(e)
	}
	for _, e := range state.GetHistory() {
		s.AddToHistory(e)
	}
	s.CustomStatus = state.CustomStatus
	s.Generation = state.Generation
}

func getMultiEntryKeyName(prefix string, i uint64) string {
	return fmt.Sprintf("%s-%06d", prefix, i)
}
