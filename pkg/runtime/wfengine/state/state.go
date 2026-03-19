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
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/logger"
)

const (
	inboxKeyPrefix     = "inbox"
	historyKeyPrefix   = "history"
	sigcertKeyPrefix   = "sigcert"
	signatureKeyPrefix = "signature"
	customStatusKey    = "customStatus"
	metadataKey        = "metadata"
)

var wfLogger = logger.NewLogger("dapr.runtime.actor.target.workflow.state")

type Options struct {
	AppID             string
	WorkflowActorType string
	ActivityActorType string
	Signer            *signer.Signer
}

type State struct {
	appID             string
	workflowActorType string
	activityActorType string

	Inbox               []*backend.HistoryEvent
	History             []*backend.HistoryEvent
	SigningCertificates []*backend.SigningCertificate
	Signatures          []*backend.HistorySignature
	CustomStatus        *wrapperspb.StringValue
	Generation          uint64

	// RawHistory holds the raw bytes of history events as loaded from the
	// state store. These are used for signature verification (to verify
	// against actual persisted bytes) and for catch-up signing when a
	// workflow transitions from a non-signing to a signing host.
	RawHistory [][]byte

	// marshaledNewHistory holds deterministically marshaled bytes for newly
	// added history events, set by SetMarshaledNewHistory. When set, these
	// exact bytes are persisted to the state store (matching what was signed).
	marshaledNewHistory [][]byte

	// change tracking
	inboxAddedCount                 int
	inboxRemovedCount               int
	historyAddedCount               int
	historyRemovedCount             int
	signingCertificatesAddedCount   int
	signingCertificatesRemovedCount int
	signaturesAddedCount            int
	signaturesRemovedCount          int
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
	s.RawHistory = nil
	s.signingCertificatesAddedCount = 0
	s.signingCertificatesRemovedCount += len(s.SigningCertificates)
	s.SigningCertificates = nil
	s.signaturesAddedCount = 0
	s.signaturesRemovedCount += len(s.Signatures)
	s.Signatures = nil
	s.CustomStatus = nil
	s.Generation++
}

// ResetChangeTracking resets the change tracking counters. This should be called after a save request.
func (s *State) ResetChangeTracking() {
	s.inboxAddedCount = 0
	s.inboxRemovedCount = 0
	s.historyAddedCount = 0
	s.historyRemovedCount = 0
	s.signingCertificatesAddedCount = 0
	s.signingCertificatesRemovedCount = 0
	s.signaturesAddedCount = 0
	s.signaturesRemovedCount = 0
	s.marshaledNewHistory = nil
	s.RawHistory = nil
}

func (s *State) ApplyRuntimeStateChanges(rs *backend.OrchestrationRuntimeState) {
	if rs.GetContinuedAsNew() {
		s.historyRemovedCount += len(s.History)
		s.historyAddedCount = 0
		s.History = nil
		s.signingCertificatesRemovedCount += len(s.SigningCertificates)
		s.signingCertificatesAddedCount = 0
		s.SigningCertificates = nil
		s.signaturesRemovedCount += len(s.Signatures)
		s.signaturesAddedCount = 0
		s.Signatures = nil
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

func (s *State) AddSigningCertificate(cert *backend.SigningCertificate) {
	s.SigningCertificates = append(s.SigningCertificates, cert)
	s.signingCertificatesAddedCount++
}

func (s *State) AddSignature(sig *backend.HistorySignature) {
	s.Signatures = append(s.Signatures, sig)
	s.signaturesAddedCount++
}

// SetMarshaledNewHistory stores pre-marshaled bytes for newly added history
// events. These bytes will be used by GetSaveRequest instead of re-marshaling,
// ensuring the persisted bytes match exactly what was signed.
func (s *State) SetMarshaledNewHistory(raw [][]byte) {
	s.marshaledNewHistory = raw
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
	opsCapacity := s.inboxAddedCount + s.inboxRemovedCount +
		s.historyAddedCount + s.historyRemovedCount +
		s.signingCertificatesAddedCount + s.signingCertificatesRemovedCount +
		s.signaturesAddedCount + s.signaturesRemovedCount +
		2 // customStatus + metadata
	req := &api.TransactionalRequest{
		ActorType:  s.workflowActorType,
		ActorID:    actorID,
		Operations: make([]api.TransactionalOperation, 0, opsCapacity),
	}

	if err := addStateOperations(req, inboxKeyPrefix, s.Inbox, s.inboxAddedCount, s.inboxRemovedCount); err != nil {
		return nil, err
	}

	if err := s.addHistoryOperations(req); err != nil {
		return nil, err
	}

	if err := addProtoStateOperations(req, sigcertKeyPrefix, s.SigningCertificates, s.signingCertificatesAddedCount, s.signingCertificatesRemovedCount); err != nil {
		return nil, err
	}

	if err := addProtoStateOperations(req, signatureKeyPrefix, s.Signatures, s.signaturesAddedCount, s.signaturesRemovedCount); err != nil {
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
		InboxLength:              uint64(len(s.Inbox)),
		HistoryLength:            uint64(len(s.History)),
		Generation:               s.Generation,
		SignatureLength:          uint64(len(s.Signatures)),
		SigningCertificateLength: uint64(len(s.SigningCertificates)),
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

// addHistoryOperations adds upsert/delete operations for history events. If
// marshaledNewHistory was set (by the signing flow), those pre-marshaled bytes
// are used directly so the persisted bytes match what was signed. Otherwise
// events are marshaled with proto.Marshal.
func (s *State) addHistoryOperations(req *api.TransactionalRequest) error {
	start := len(s.History) - s.historyAddedCount
	for i := start; i < len(s.History); i++ {
		var data []byte
		var err error
		localIdx := i - start
		if localIdx < len(s.marshaledNewHistory) {
			data = s.marshaledNewHistory[localIdx]
		} else {
			data, err = proto.Marshal(s.History[i])
			if err != nil {
				return err
			}
		}

		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			//nolint:gosec
			Request: api.TransactionalUpsert{Key: getMultiEntryKeyName(historyKeyPrefix, uint64(i)), Value: data},
		})
	}

	for i := len(s.History); i < s.historyRemovedCount; i++ {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: getMultiEntryKeyName(historyKeyPrefix, uint64(i))},
		})
	}

	return nil
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

// addProtoStateOperations persists newly added proto messages and deletes removed ones.
func addProtoStateOperations[T proto.Message](req *api.TransactionalRequest, keyPrefix string, items []T, addedCount int, removedCount int) error {
	for i := len(items) - addedCount; i < len(items); i++ {
		data, err := proto.Marshal(items[i])
		if err != nil {
			return err
		}

		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			//nolint:gosec
			Request: api.TransactionalUpsert{Key: getMultiEntryKeyName(keyPrefix, uint64(i)), Value: data},
		})
	}

	for i := len(items); i < removedCount; i++ {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, uint64(i))},
		})
	}

	return nil
}

func addPurgeStateOperations(req *api.TransactionalRequest, keyPrefix string, count int) {
	for i := range count {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: getMultiEntryKeyName(keyPrefix, uint64(i))},
		})
	}
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

	var metadata backend.WorkflowStateMetadata
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

	// Load inbox, history, signing certs, signatures, and custom status using a bulk request
	wState := NewState(opts)
	wState.Generation = metadata.GetGeneration()
	wState.Inbox = make([]*backend.HistoryEvent, 0, metadata.GetInboxLength())
	wState.History = make([]*backend.HistoryEvent, 0, metadata.GetHistoryLength())
	wState.RawHistory = make([][]byte, 0, metadata.GetHistoryLength())
	wState.SigningCertificates = make([]*backend.SigningCertificate, 0, metadata.GetSigningCertificateLength())
	wState.Signatures = make([]*backend.HistorySignature, 0, metadata.GetSignatureLength())

	totalKeys := metadata.GetInboxLength() + metadata.GetHistoryLength() +
		metadata.GetSigningCertificateLength() + metadata.GetSignatureLength() + 1
	bulkReq := &api.GetBulkStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		Keys:      make([]string, totalKeys),
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

	for i := range metadata.GetSigningCertificateLength() {
		bulkReq.Keys[n] = getMultiEntryKeyName(sigcertKeyPrefix, i)
		n++
	}

	for i := range metadata.GetSignatureLength() {
		bulkReq.Keys[n] = getMultiEntryKeyName(signatureKeyPrefix, i)
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
		wState.RawHistory = append(wState.RawHistory, bulkRes[key])
	}

	for i := range metadata.GetSigningCertificateLength() {
		key = getMultiEntryKeyName(sigcertKeyPrefix, i)
		if bulkRes[key] == nil {
			wfLogger.Warnf("Failed to load signing cert state key '%s': not found", key)
			continue
		}

		var cert backend.SigningCertificate
		err = proto.Unmarshal(bulkRes[key], &cert)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal signing certificate from state key '%s': %w", key, err)
		}

		wState.SigningCertificates = append(wState.SigningCertificates, &cert)
	}

	for i := range metadata.GetSignatureLength() {
		key = getMultiEntryKeyName(signatureKeyPrefix, i)
		if bulkRes[key] == nil {
			wfLogger.Warnf("Failed to load signature state key '%s': not found", key)
			continue
		}

		var sig backend.HistorySignature
		err = proto.Unmarshal(bulkRes[key], &sig)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history signature from state key '%s': %w", key, err)
		}

		wState.Signatures = append(wState.Signatures, &sig)
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

	wfLogger.Debugf("%s: loaded %d state records in %v", actorID, 1+len(bulkRes), time.Since(loadStartTime))

	if len(wState.Signatures) > 0 {
		if opts.Signer == nil {
			wfLogger.Warnf("Workflow '%s' has signed history but no signer is configured; signature verification skipped", actorID)
		} else if err := verifySignatureChain(wState, opts.Signer); err != nil {
			return wState, &VerificationError{
				Err: fmt.Errorf("workflow history signature verification failed for '%s': %w", actorID, err),
			}
		}
	}

	return wState, nil
}

func verifySignatureChain(s *State, sgn *signer.Signer) error {
	rawEvents := s.RawHistory
	if len(s.Signatures) > 0 {
		last := s.Signatures[len(s.Signatures)-1]
		coveredEvents := last.GetStartEventIndex() + last.GetEventCount()
		if coveredEvents < uint64(len(rawEvents)) {
			rawEvents = rawEvents[:coveredEvents]
		}
	}
	return historysigning.VerifyChain(historysigning.VerifyChainOptions{
		Signatures:   s.Signatures,
		Certs:        s.SigningCertificates,
		AllRawEvents: rawEvents,
		Signer:       sgn,
	})
}

func (s *State) GetPurgeRequest(actorID string) (*api.TransactionalRequest, error) {
	req := &api.TransactionalRequest{
		ActorType: s.workflowActorType,
		ActorID:   actorID,
		// Initial capacity should be enough to contain the entire inbox, history, signing data, and custom status + metadata
		Operations: make([]api.TransactionalOperation, 0, len(s.Inbox)+len(s.History)+len(s.SigningCertificates)+len(s.Signatures)+2),
	}

	addPurgeStateOperations(req, inboxKeyPrefix, len(s.Inbox))
	addPurgeStateOperations(req, historyKeyPrefix, len(s.History))
	addPurgeStateOperations(req, sigcertKeyPrefix, len(s.SigningCertificates))
	addPurgeStateOperations(req, signatureKeyPrefix, len(s.Signatures))

	req.Operations = append(req.Operations,
		api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: customStatusKey},
		},
		api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: metadataKey},
		},
	)

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
	s := strconv.FormatUint(i, 10)
	const padLen = 6
	var b strings.Builder
	b.Grow(len(prefix) + 1 + padLen)
	b.WriteString(prefix)
	b.WriteByte('-')
	for j := len(s); j < padLen; j++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
	return b.String()
}
