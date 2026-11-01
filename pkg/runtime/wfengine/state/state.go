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
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/state"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/logger"
)

const (
	inboxKeyPrefix      = "inbox"
	historyKeyPrefix    = "history"
	sigcertKeyPrefix    = "sigcert"
	extSigCertKeyPrefix = "ext-sigcert"
	signatureKeyPrefix  = "signature"
	customStatusKey     = "customStatus"
	// MetadataKey is the state-store key holding the workflow's metadata row
	// (history/inbox/signature lengths + generation). Exported so other
	// packages can refresh just this row to pick up the latest ETag without
	// reloading the full workflow state.
	MetadataKey = "metadata"
	// ParentNotifyKey is the row whose presence records that a completed
	// child still owes its parent the completion.
	ParentNotifyKey      = "parent-notify"
	propagatedHistoryKey = "propagated-history"
	creationInputKey     = "creation-input"
	// encryptionKey holds the instance's wrapped payload encryption key. It
	// is present if and only if the instance's payloads are sealed.
	encryptionKey = "encryption"

	// maxStateEntries is the upper bound for any metadata count field
	// (inbox, history, signatures, own signing certificates, external
	// signing certificates). This prevents OOM from an attacker inflating
	// metadata values in the state store.
	maxStateEntries = 1_000_000
)

var wfLogger = logger.NewLogger("dapr.runtime.actor.target.workflow.state")

type Options struct {
	AppID             string
	Namespace         string
	WorkflowActorType string
	ActivityActorType string
	Signer            *signer.Signer
	// Encryptor opens the keys of encrypted instances on load. Nil when
	// payload encryption is off.
	Encryptor *encryption.Encryptor
}

type State struct {
	appID             string
	namespace         string
	workflowActorType string
	activityActorType string

	Inbox                       []*backend.HistoryEvent
	History                     []*backend.HistoryEvent
	SigningCertificates         []*backend.SigningCertificate
	ExternalSigningCertificates []*backend.ExternalSigningCertificate
	Signatures                  []*backend.HistorySignature
	CustomStatus                *wrapperspb.StringValue
	Generation                  uint64

	// RawHistory holds the raw bytes of history events as loaded from the
	// state store. These are used for signature verification to verify
	// against the actual persisted bytes.
	RawHistory [][]byte

	// RawSignatures holds the raw serialized bytes of each HistorySignature
	// as loaded from the state store or returned by SignResult.RawSignature.
	// These are the single source of truth for digest computation in chain
	// linking and must be preserved exactly as stored.
	RawSignatures [][]byte

	// marshaledNewHistory holds deterministically marshaled bytes for newly
	// added history events, set by SetMarshaledNewHistory. When set, these
	// exact bytes are persisted to the state store (matching what was signed).
	marshaledNewHistory [][]byte

	// IncomingHistory is the propagated history this workflow received from
	// its caller. Stored as a separate state key, NOT as part of history
	// events. Set once at workflow creation, never modified.
	IncomingHistory *protos.PropagatedHistory

	// ParentNotifyPending is set by the terminal save of a child workflow and
	// cleared once the parent acknowledged the completion, so a crash or a
	// failed delivery in between re-sends from durable history.
	ParentNotifyPending bool

	// CreationInput is the input the parent created this child with: the
	// parent verifies the completion attestation against it, while after a
	// ContinueAsNew the current start event carries the continued input.
	// Recorded at creation for signed children only, since only the
	// attestation reads it; nil otherwise. It is stamped with the creating
	// parent so a row an older binary's purge left behind is not read into a
	// recreated instance.
	CreationInput       *wrapperspb.StringValue
	creationInputParent creationParent
	keepCreationInput   bool

	// externalCertDigestIndex maps SHA-256 cert digests (raw 32-byte digest
	// converted to a string for use as a map key, not hex-encoded) to their
	// index in ExternalSigningCertificates. Built on load and maintained
	// incrementally by AddExternalCert so repeated ingestion of the same foreign
	// cert within one run is O(1) and idempotent. Not persisted. Rebuilt from
	// ExternalSigningCertificates at load time.
	externalCertDigestIndex map[string]uint64

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
	// metadataETagSeen records that the store has returned an ETag for the
	// metadata row. A nil metadataETag after that means the row's version
	// was lost (the row deleted underneath us), and a save must not fall
	// through to a blind upsert that would resurrect purged state.
	metadataETagSeen bool

	// customStatusPersisted and propagatedHistoryPersisted are observed at load
	// time from the state store's ETag for those keys. They are the source of
	// truth for "does this optional key currently exist in the store" at purge
	// time, independent of whether the in-memory value is populated (a key can
	// be persisted with an empty proto, which would load as a nil CustomStatus /
	// IncomingHistory).
	customStatusPersisted      bool
	propagatedHistoryPersisted bool
	parentNotifyPersisted      bool
	parentNotifyChanged        bool
	creationInputPersisted     bool
	creationInputChanged       bool

	// EncryptionKey seals the instance's payloads; nil while the instance is
	// stored in plaintext. Exported so code that persists or reads workflow
	// state outside this package can seal and unseal with it.
	EncryptionKey *encryption.InstanceKey
	// aadBase binds sealed values to this instance: "<actor type>||<id>".
	aadBase string
	// historySealedFrom is the first sealed history index. History rows
	// below it predate encryption and are signed, so they are kept as
	// written.
	historySealedFrom   uint64
	encryptionPersisted bool
	encryptionChanged   bool
	encryptionRewrap    bool
	// sealRewritePending makes the next save rewrite every plaintext payload
	// row sealed: set when an instance loaded in plaintext gets its key.
	sealRewritePending bool
	// loaded is set when the state was read from the store.
	loaded bool

	// change tracking
	inboxAddedCount                         int
	inboxRemovedCount                       int
	historyAddedCount                       int
	historyRemovedCount                     int
	signingCertificatesAddedCount           int
	signingCertificatesRemovedCount         int
	externalSigningCertificatesAddedCount   int
	externalSigningCertificatesRemovedCount int
	signaturesAddedCount                    int
	signaturesRemovedCount                  int
	incomingHistoryChanged                  bool
	customStatusChanged                     bool
}

// TODO: @joshvanl: remove in v1.16
type legacyWorkflowStateMetadata struct {
	InboxLength   uint64
	HistoryLength uint64
	Generation    uint64
}

// ErrMetadataETagLost is returned by GetSaveRequest when the metadata row's
// ETag was known and is now nil: the row was deleted underneath the cached
// state, so a blind upsert would resurrect purged state.
var ErrMetadataETagLost = errors.New("workflow metadata etag lost after load; refusing blind upsert")

func NewState(opts Options) *State {
	return &State{
		Generation:        1,
		appID:             opts.AppID,
		namespace:         opts.Namespace,
		workflowActorType: opts.WorkflowActorType,
		activityActorType: opts.ActivityActorType,
		keepCreationInput: opts.Signer != nil,
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
	s.externalSigningCertificatesAddedCount = 0
	s.externalSigningCertificatesRemovedCount += len(s.ExternalSigningCertificates)
	s.ExternalSigningCertificates = nil
	s.externalCertDigestIndex = nil
	s.signaturesAddedCount = 0
	s.signaturesRemovedCount += len(s.Signatures)
	s.Signatures = nil
	s.RawSignatures = nil
	if s.CustomStatus.GetValue() != "" {
		s.customStatusChanged = true
	}
	s.CustomStatus = nil
	if s.IncomingHistory != nil {
		s.IncomingHistory = nil
		s.incomingHistoryChanged = true
	}
	if s.ParentNotifyPending || s.parentNotifyPersisted {
		s.ParentNotifyPending = false
		s.parentNotifyChanged = true
	}
	if s.CreationInput != nil || s.creationInputPersisted {
		s.CreationInput = nil
		s.creationInputChanged = true
	}
	s.historyReplaced()
	// A stored key that was not opened (a tamper-marked instance) goes with
	// the rows it sealed.
	if s.EncryptionKey == nil && s.encryptionPersisted {
		s.encryptionChanged = true
	}
	s.Generation++
}

// ResetChangeTracking resets the change tracking counters. This should be called after a save request.
func (s *State) ResetChangeTracking() {
	// Mirrors the customStatus write condition in GetSaveRequest: after a
	// successful save that carried the key, it is persisted and clean.
	if (s.historyAddedCount > 0 || s.historyRemovedCount > 0) && (s.customStatusChanged || !s.customStatusPersisted) {
		s.customStatusPersisted = true
		s.customStatusChanged = false
	}
	// A save with incomingHistoryChanged either upserts or deletes the
	// propagated-history key; track the resulting persistence state.
	if s.incomingHistoryChanged {
		s.propagatedHistoryPersisted = s.IncomingHistory != nil
	}
	if s.parentNotifyChanged {
		s.parentNotifyPersisted = s.ParentNotifyPending
		s.parentNotifyChanged = false
	}
	if s.creationInputChanged {
		s.creationInputPersisted = s.CreationInput != nil
		s.creationInputChanged = false
	}
	if s.encryptionChanged {
		s.encryptionPersisted = s.EncryptionKey != nil
		s.encryptionChanged = false
	}
	s.sealRewritePending = false

	s.inboxAddedCount = 0
	s.inboxRemovedCount = 0
	s.historyAddedCount = 0
	s.historyRemovedCount = 0
	s.signingCertificatesAddedCount = 0
	s.signingCertificatesRemovedCount = 0
	s.externalSigningCertificatesAddedCount = 0
	s.externalSigningCertificatesRemovedCount = 0
	s.signaturesAddedCount = 0
	s.signaturesRemovedCount = 0
	s.marshaledNewHistory = nil
	s.RawHistory = nil
	s.incomingHistoryChanged = false
}

// SetIncomingHistory sets the received propagated history on the state.
func (s *State) SetIncomingHistory(ph *protos.PropagatedHistory) {
	s.IncomingHistory = ph
	s.incomingHistoryChanged = true
}

// creationParent identifies the creation a kept input belongs to.
type creationParent struct {
	InstanceID  string `json:"parentInstance"`
	ExecutionID string `json:"parentExecution"`
}

// creationInputRow is the persisted form of the creation input.
type creationInputRow struct {
	creationParent
	Input string `json:"input"`
}

func creationParentOf(p *protos.ParentInstanceInfo) creationParent {
	return creationParent{
		InstanceID:  p.GetWorkflowInstance().GetInstanceId(),
		ExecutionID: p.GetWorkflowInstance().GetExecutionId().GetValue(),
	}
}

// setCreationInput keeps the input the parent created this child with across
// ContinueAsNew. A nil input is kept as an empty value so the rebuild can
// tell "no input" from "not recorded".
func (s *State) setCreationInput(in *wrapperspb.StringValue, parent *protos.ParentInstanceInfo) {
	if in == nil {
		in = &wrapperspb.StringValue{}
	}
	s.CreationInput = in
	s.creationInputParent = creationParentOf(parent)
	s.creationInputChanged = true
}

// KeepCreationInput records the input a signed child is created with, from
// its start event, before the first save. A root workflow or an unsigned
// child keeps nothing.
func (s *State) KeepCreationInput(start *protos.ExecutionStartedEvent) {
	if !s.keepCreationInput || start.GetParentInstance() == nil {
		return
	}
	s.setCreationInput(start.GetInput(), start.GetParentInstance())
}

// CreationInputFor reports whether the kept creation input belongs to the
// creation described by parent.
func (s *State) CreationInputFor(parent *protos.ParentInstanceInfo) bool {
	return s.CreationInput != nil && s.creationInputParent == creationParentOf(parent)
}

// ClearCreationInput drops a kept creation input that does not belong to
// this instance's creation; the next save deletes the row.
func (s *State) ClearCreationInput() {
	if s.CreationInput != nil || s.creationInputPersisted {
		s.CreationInput = nil
		s.creationInputParent = creationParent{}
		s.creationInputChanged = true
	}
}

// executionStartedOf returns the instance's ExecutionStarted event: from
// history, or from the inbox when the first turn has not committed it yet.
func executionStartedOf(history, inbox []*protos.HistoryEvent) *protos.ExecutionStartedEvent {
	for _, events := range [][]*protos.HistoryEvent{history, inbox} {
		for _, e := range events {
			if es := e.GetExecutionStarted(); es != nil {
				return es
			}
		}
	}
	return nil
}

// SetParentNotifyPending records whether the parent still has to be told
// about this workflow's completion.
func (s *State) SetParentNotifyPending(pending bool) {
	if s.ParentNotifyPending != pending || (pending && !s.parentNotifyPersisted) {
		s.parentNotifyChanged = true
	}
	s.ParentNotifyPending = pending
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
// MetadataETagSeen reports whether the store has ever returned an ETag for
// this instance's metadata row, so a missing row can be told from a store
// that has not served the row yet.
func (s *State) MetadataETagSeen() bool { return s.metadataETagSeen }

func (s *State) SetMetadataETag(etag *string) {
	s.metadataETag = etag
	if etag != nil {
		s.metadataETagSeen = true
	}
}

func (s *State) ApplyRuntimeStateChanges(rs *backend.WorkflowRuntimeState) {
	if rs.GetContinuedAsNew() {
		s.historyRemovedCount += len(s.History)
		s.historyAddedCount = 0
		s.History = nil
		s.signingCertificatesRemovedCount += len(s.SigningCertificates)
		s.signingCertificatesAddedCount = 0
		s.SigningCertificates = nil
		s.externalSigningCertificatesRemovedCount += len(s.ExternalSigningCertificates)
		s.externalSigningCertificatesAddedCount = 0
		s.ExternalSigningCertificates = nil
		s.externalCertDigestIndex = nil
		s.signaturesRemovedCount += len(s.Signatures)
		s.signaturesAddedCount = 0
		s.Signatures = nil
		s.RawSignatures = nil
		s.historyReplaced()
	}

	newHistoryEvents := rs.GetNewEvents()
	s.History = append(s.History, newHistoryEvents...)
	s.historyAddedCount += len(newHistoryEvents)

	// nil and empty CustomStatus persist as the same bytes, so compare by
	// value: only an actual change dirties the flag.
	newCustomStatus := rs.GetCustomStatus()
	if s.CustomStatus.GetValue() != newCustomStatus.GetValue() {
		s.customStatusChanged = true
	}
	s.CustomStatus = newCustomStatus
}

func (s *State) AddToInbox(e *backend.HistoryEvent) {
	s.Inbox = append(s.Inbox, e)
	s.inboxAddedCount++
}

func (s *State) AddToHistory(e *backend.HistoryEvent) {
	s.History = append(s.History, e)
	s.historyAddedCount++
}

// FindHistoryEventByID returns the history event whose EventId matches
// id, or nil if no such event exists. Linear scan; small constant
// number of TaskScheduled / ChildWorkflowInstanceCreated lookups per
// inbound completion event.
func (s *State) FindHistoryEventByID(id int32) *backend.HistoryEvent {
	for _, e := range s.History {
		if e.GetEventId() == id {
			return e
		}
	}
	return nil
}

func (s *State) AddSigningCertificate(cert *backend.SigningCertificate) {
	s.SigningCertificates = append(s.SigningCertificates, cert)
	s.signingCertificatesAddedCount++
}

// AddExternalCert appends a foreign signing certificate to the ext-sigcert
// table if its digest is not already present, and returns the table index.
// Idempotent: the same (digest, certDER) pair called twice in one run
// produces exactly one stored entry and returns the same index both times.
// digest must be the SHA-256 of certDER (the caller typically already has
// this value from historysigning.CertDigest). The caller is responsible
// for verifying that the digest matches certDER before calling - this
// method does not re-hash on the hot path. Returns an error if digest is
// not 32 bytes.
func (s *State) AddExternalCert(digest []byte, certDER []byte) (uint64, error) {
	if len(digest) != sha256.Size {
		return 0, fmt.Errorf("AddExternalCert: digest must be %d bytes, got %d", sha256.Size, len(digest))
	}
	if s.externalCertDigestIndex == nil {
		s.externalCertDigestIndex = make(map[string]uint64, len(s.ExternalSigningCertificates)+1)
	}
	key := string(digest)
	if idx, ok := s.externalCertDigestIndex[key]; ok {
		return idx, nil
	}
	idx := uint64(len(s.ExternalSigningCertificates))
	// Defensive-copy both byte slices so the stored entry does not alias
	// caller buffers. Proto getters often return references into the
	// enclosing message's backing array, which the caller may overwrite
	// later (e.g., when the source event is reused or its
	// SignerCertificate companion is cleared).
	storedDigest := make([]byte, len(digest))
	copy(storedDigest, digest)
	storedCert := make([]byte, len(certDER))
	copy(storedCert, certDER)
	s.ExternalSigningCertificates = append(s.ExternalSigningCertificates, &backend.ExternalSigningCertificate{
		Digest:      storedDigest,
		Certificate: storedCert,
	})
	s.externalSigningCertificatesAddedCount++
	s.externalCertDigestIndex[key] = idx
	return idx, nil
}

// LookupExternalCert returns the ExternalSigningCertificate whose digest
// matches the given bytes, plus its table index. Returns (nil, 0, false)
// if not found. Used by attestation verification to resolve a payload's
// signerCertDigest to the corresponding cert.
func (s *State) LookupExternalCert(digest []byte) (*backend.ExternalSigningCertificate, uint64, bool) {
	if s.externalCertDigestIndex == nil {
		return nil, 0, false
	}
	idx, ok := s.externalCertDigestIndex[string(digest)]
	if !ok {
		return nil, 0, false
	}
	return s.ExternalSigningCertificates[idx], idx, true
}

// setLoadedExternalCerts populates the external signing certificate table
// and rebuilds the digest→index map from entries freshly loaded from the
// state store. Each entry's stored digest must match sha256 of its
// certificate bytes - otherwise the state store has been tampered with
// (or bit-rotted) since the entry was written.
//
// This is the load-time counterpart to AddExternalCert: AddExternalCert
// incrementally grows the table during a run; setLoadedExternalCerts
// restores the full table from persisted state at the start of a run.
// Both leave the state with a consistent ExternalSigningCertificates
// slice + externalCertDigestIndex map; the difference is only *when* the
// table is populated (ingestion vs. load).
func (s *State) setLoadedExternalCerts(certs []*backend.ExternalSigningCertificate) error {
	for i, ext := range certs {
		if got := historysigning.CertDigest(ext.GetCertificate()); !bytes.Equal(got, ext.GetDigest()) {
			return fmt.Errorf("external signing certificate index %d: stored digest does not match sha256 of certificate bytes", i)
		}
	}
	s.ExternalSigningCertificates = certs
	if len(certs) == 0 {
		s.externalCertDigestIndex = nil
		return nil
	}
	s.externalCertDigestIndex = make(map[string]uint64, len(certs))
	for i, ext := range certs {
		s.externalCertDigestIndex[string(ext.GetDigest())] = uint64(i)
	}
	return nil
}

func (s *State) AddSignature(sig *backend.HistorySignature, raw []byte) {
	s.Signatures = append(s.Signatures, sig)
	s.RawSignatures = append(s.RawSignatures, raw)
	s.signaturesAddedCount++
}

// HistoryAddedCount returns the number of history events added since the
// last save or reset. Used by the orchestrator to know how many events
// need signing.
func (s *State) HistoryAddedCount() int {
	return s.historyAddedCount
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

// SetEncryptionKey sets the key that seals the instance's payloads. The
// first key set on an instance loaded in plaintext makes the next save seal
// it: an unsigned instance has every row rewritten sealed, while a signed one
// keeps its signed history rows as written and seals from the next event on.
func (s *State) SetEncryptionKey(actorID string, k *encryption.InstanceKey) {
	legacy := s.EncryptionKey == nil && s.loaded && !s.encryptionPersisted
	s.EncryptionKey = k
	s.aadBase = s.workflowActorType + "||" + actorID
	s.encryptionChanged = true
	s.encryptionRewrap = false
	if legacy {
		s.sealRewritePending = true
		if len(s.Signatures) > 0 {
			//nolint:gosec
			s.historySealedFrom = uint64(len(s.History) - s.historyAddedCount)
		}
	}
}

// EncryptionKeyUnopened reports whether the instance has a stored payload key
// that was not opened on load (a tamper-marked instance). Its sealed rows are
// only readable with that key, so it must not be replaced.
func (s *State) EncryptionKeyUnopened() bool {
	return s.EncryptionKey == nil && s.encryptionPersisted && !s.encryptionChanged
}

// EncryptionRewrapNeeded reports whether the instance key was wrapped by a
// key encryption key other than the configured one.
func (s *State) EncryptionRewrapNeeded() bool {
	return s.encryptionRewrap
}

// SealedHistoryEvent returns history event i as it is stored: with its
// payload fields sealed when the instance is encrypted.
func (s *State) SealedHistoryEvent(i int) (*backend.HistoryEvent, error) {
	e := s.History[i]
	//nolint:gosec
	if s.EncryptionKey == nil || uint64(i) < s.historySealedFrom || IsTamperMarker(e) {
		return e, nil
	}
	//nolint:gosec
	return encryption.SealMessage(s.EncryptionKey, s.rowAAD(getMultiEntryKeyName(historyKeyPrefix, uint64(i))), e)
}

// historyReplaced records that the whole history is rewritten in the next
// save, so every history row is sealed from index 0.
func (s *State) historyReplaced() {
	if s.historySealedFrom != 0 {
		s.historySealedFrom = 0
		s.encryptionChanged = true
	}
}

func (s *State) rowAAD(rowKey string) string {
	return s.aadBase + "||" + rowKey
}

// sealInbox returns inbox with entries from index from on sealed.
func (s *State) sealInbox(inbox []*backend.HistoryEvent, from int) ([]*backend.HistoryEvent, error) {
	if s.EncryptionKey == nil {
		return inbox, nil
	}
	sealed := slices.Clone(inbox)
	for i := from; i < len(sealed); i++ {
		var err error
		//nolint:gosec
		if sealed[i], err = encryption.SealMessage(s.EncryptionKey, s.rowAAD(getMultiEntryKeyName(inboxKeyPrefix, uint64(i))), sealed[i]); err != nil {
			return nil, err
		}
	}
	return sealed, nil
}

// unsealAll unseals the payloads of a loaded encrypted instance in place.
func (s *State) unsealAll() error {
	k := s.EncryptionKey
	for i, e := range s.Inbox {
		key := getMultiEntryKeyName(inboxKeyPrefix, uint64(i))
		if err := encryption.UnsealMessage(k, s.rowAAD(key), e); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	if s.historySealedFrom > uint64(len(s.History)) {
		return fmt.Errorf("history is sealed from event %d but has %d events", s.historySealedFrom, len(s.History))
	}
	for i := s.historySealedFrom; i < uint64(len(s.History)); i++ {
		if IsTamperMarker(s.History[i]) {
			continue
		}
		key := getMultiEntryKeyName(historyKeyPrefix, i)
		if err := encryption.UnsealMessage(k, s.rowAAD(key), s.History[i]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	if s.CustomStatus != nil {
		v, err := k.UnsealString(s.rowAAD(customStatusKey), s.CustomStatus.GetValue())
		if err != nil {
			return fmt.Errorf("%s: %w", customStatusKey, err)
		}
		s.CustomStatus.Value = v
	}
	if s.CreationInput != nil {
		v, err := k.UnsealString(s.rowAAD(creationInputKey), s.CreationInput.GetValue())
		if err != nil {
			return fmt.Errorf("%s: %w", creationInputKey, err)
		}
		s.CreationInput.Value = v
	}
	if s.IncomingHistory != nil {
		if err := encryption.UnsealMessage(k, s.rowAAD(propagatedHistoryKey), s.IncomingHistory); err != nil {
			return fmt.Errorf("%s: %w", propagatedHistoryKey, err)
		}
	}
	return nil
}

// persistableInbox returns the inbox without timer events, which are never
// written as inbox keys. Unlike app-delivered events (whose durable home is
// their inbox row, written before their trigger reminder), a TimerFired's
// durable home is the scheduler job itself: the job carries the full event
// payload and is only acked — and deleted — after the run that consumed the
// event has committed it to history. Until then any failure or stall leaves
// the job un-acked and the scheduler re-delivers it, so an inbox row would be
// a second delivery source for the same event, not added durability.
// Timer events can still sit in s.Inbox when a save happens mid-run (workflow
// stall, abandoned continue-as-new); persisting or counting them would declare
// inbox keys that don't exist in the store, making every subsequent
// LoadWorkflowState fail.
func (s *State) persistableInbox() []*backend.HistoryEvent {
	persistable := make([]*backend.HistoryEvent, 0, len(s.Inbox))
	for _, e := range s.Inbox {
		if e.GetTimerFired() == nil {
			persistable = append(persistable, e)
		}
	}
	return persistable
}

func (s *State) GetSaveRequest(actorID string) (*api.TransactionalRequest, error) {
	// TODO: Batching up the save requests into smaller chunks to avoid batch size limits in Dapr state stores.
	opsCapacity := s.inboxAddedCount + s.inboxRemovedCount +
		s.historyAddedCount + s.historyRemovedCount +
		s.signingCertificatesAddedCount + s.signingCertificatesRemovedCount +
		s.externalSigningCertificatesAddedCount + s.externalSigningCertificatesRemovedCount +
		s.signaturesAddedCount + s.signaturesRemovedCount +
		5 // customStatus + metadata + parent-notify + creation-input + encryption
	req := &api.TransactionalRequest{
		ActorType:  s.workflowActorType,
		ActorID:    actorID,
		Operations: make([]api.TransactionalOperation, 0, opsCapacity),
	}

	inbox := s.persistableInbox()
	inboxWritten := s.inboxAddedCount
	if s.sealRewritePending {
		inboxWritten = len(inbox)
	}
	sealedInbox, serr := s.sealInbox(inbox, len(inbox)-inboxWritten)
	if serr != nil {
		return nil, fmt.Errorf("failed to seal inbox: %w", serr)
	}
	if err := addStateOperations(req, inboxKeyPrefix, sealedInbox, inboxWritten, s.inboxRemovedCount); err != nil {
		return nil, err
	}

	if err := s.addHistoryOperations(req); err != nil {
		return nil, err
	}

	if err := addProtoStateOperations(req, sigcertKeyPrefix, s.SigningCertificates, s.signingCertificatesAddedCount, s.signingCertificatesRemovedCount); err != nil {
		return nil, err
	}

	if err := addProtoStateOperations(req, extSigCertKeyPrefix, s.ExternalSigningCertificates, s.externalSigningCertificatesAddedCount, s.externalSigningCertificatesRemovedCount); err != nil {
		return nil, err
	}

	if err := addRawBytesStateOperations(req, signatureKeyPrefix, s.RawSignatures, s.signaturesAddedCount, s.signaturesRemovedCount); err != nil {
		return nil, err
	}

	// We update the custom status only when the workflow itself has been updated, and not when
	// we're saving changes only to the workflow inbox. Re-writes of an
	// unchanged status are skipped once the key is persisted; the
	// customStatusChanged flag is set wherever CustomStatus mutates.
	if ((s.historyAddedCount > 0 || s.historyRemovedCount > 0) && (s.customStatusChanged || !s.customStatusPersisted)) ||
		(s.sealRewritePending && s.customStatusPersisted) {
		cs := s.CustomStatus
		if cs == nil {
			cs = &wrapperspb.StringValue{}
		}
		if s.EncryptionKey != nil {
			cs = wrapperspb.String(s.EncryptionKey.SealString(s.rowAAD(customStatusKey), cs.GetValue()))
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

	if s.incomingHistoryChanged || (s.sealRewritePending && s.propagatedHistoryPersisted && s.IncomingHistory != nil) {
		if s.IncomingHistory != nil {
			ph := s.IncomingHistory
			if s.EncryptionKey != nil {
				var err error
				if ph, err = encryption.SealMessage(s.EncryptionKey, s.rowAAD(propagatedHistoryKey), ph); err != nil {
					return nil, fmt.Errorf("failed to seal incoming propagated history: %w", err)
				}
			}
			phBytes, err := proto.Marshal(ph)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal incoming propagated history: %w", err)
			}
			req.Operations = append(req.Operations, api.TransactionalOperation{
				Operation: api.Upsert,
				Request:   api.TransactionalUpsert{Key: propagatedHistoryKey, Value: phBytes},
			})
		} else {
			// Explicit delete so a recreate/reset without propagation does not
			// leave stale propagated history visible to the new instance.
			req.Operations = append(req.Operations, api.TransactionalOperation{
				Operation: api.Delete,
				Request:   api.TransactionalDelete{Key: propagatedHistoryKey},
			})
		}
	}

	if s.parentNotifyChanged {
		if s.ParentNotifyPending {
			req.Operations = append(req.Operations, api.TransactionalOperation{
				Operation: api.Upsert,
				Request:   api.TransactionalUpsert{Key: ParentNotifyKey, Value: []byte{1}},
			})
		} else if s.parentNotifyPersisted {
			req.Operations = append(req.Operations, api.TransactionalOperation{
				Operation: api.Delete,
				Request:   api.TransactionalDelete{Key: ParentNotifyKey},
			})
		}
	}
	if s.creationInputChanged || (s.sealRewritePending && s.creationInputPersisted && s.CreationInput != nil) {
		if s.CreationInput != nil {
			input := s.CreationInput.GetValue()
			if s.EncryptionKey != nil {
				input = s.EncryptionKey.SealString(s.rowAAD(creationInputKey), input)
			}
			data, err := json.Marshal(creationInputRow{creationParent: s.creationInputParent, Input: input})
			if err != nil {
				return nil, fmt.Errorf("failed to marshal creation input: %w", err)
			}
			req.Operations = append(req.Operations, api.TransactionalOperation{
				Operation: api.Upsert,
				Request:   api.TransactionalUpsert{Key: creationInputKey, Value: data},
			})
		} else if s.creationInputPersisted {
			req.Operations = append(req.Operations, api.TransactionalOperation{
				Operation: api.Delete,
				Request:   api.TransactionalDelete{Key: creationInputKey},
			})
		}
	}

	switch {
	case s.encryptionChanged && s.EncryptionKey != nil:
		w := s.EncryptionKey.Wrapped()
		w.HistorySealedFrom = s.historySealedFrom
		data, err := proto.Marshal(w)
		if err != nil {
			return nil, err
		}
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			Request:   api.TransactionalUpsert{Key: encryptionKey, Value: data},
		})
	case s.encryptionChanged && s.encryptionPersisted:
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: encryptionKey},
		})
	}

	metaProto, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		InboxLength:                      uint64(len(inbox)),
		HistoryLength:                    uint64(len(s.History)),
		Generation:                       s.Generation,
		SignatureLength:                  uint64(len(s.Signatures)),
		SigningCertificateLength:         uint64(len(s.SigningCertificates)),
		ExternalSigningCertificateLength: uint64(len(s.ExternalSigningCertificates)),
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
	// workflow, or a store that returns no ETags) and falls through to a
	// blind upsert; once an ETag was seen, losing it means the row was
	// deleted underneath us and the save is refused instead.
	if s.metadataETag == nil && s.metadataETagSeen {
		return nil, ErrMetadataETagLost
	}
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

// addHistoryOperations adds upsert/delete operations for history events. If
// marshaledNewHistory was set (by the signing flow), those pre-marshaled bytes
// are used directly so the persisted bytes match what was signed. Otherwise
// events are marshaled with proto.Marshal.
func (s *State) addHistoryOperations(req *api.TransactionalRequest) error {
	start := len(s.History) - s.historyAddedCount
	// A plaintext unsigned instance getting its key has every row rewritten
	// sealed; a signed one keeps the rows below historySealedFrom.
	from := start
	if s.sealRewritePending {
		from = min(start, int(s.historySealedFrom)) //nolint:gosec
	}

	// Events not pre-marshaled by the signing flow (nil entries are), sealed
	// when the instance is encrypted.
	toMarshal := make([]*backend.HistoryEvent, len(s.History)-from)
	var slabSize int
	for i := from; i < len(s.History); i++ {
		if localIdx := i - start; localIdx >= 0 && localIdx < len(s.marshaledNewHistory) {
			continue
		}
		e, err := s.SealedHistoryEvent(i)
		if err != nil {
			return fmt.Errorf("failed to seal history event %d: %w", i, err)
		}
		toMarshal[i-from] = e
		slabSize += proto.Size(e)
	}

	// All of those events marshal into one shared slab instead of one buffer
	// each. The value slices are subslices of the slab; a mid-append
	// reallocation leaves earlier subslices pointing at the old backing
	// array, which still holds their bytes, so they stay valid.
	slab := make([]byte, 0, slabSize)
	mo := proto.MarshalOptions{}
	for i := from; i < len(s.History); i++ {
		var data []byte
		if e := toMarshal[i-from]; e != nil {
			off := len(slab)
			var err error
			slab, err = mo.MarshalAppend(slab, e)
			if err != nil {
				return err
			}
			data = slab[off:len(slab):len(slab)]
		} else {
			data = s.marshaledNewHistory[i-start]
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
	// One shared slab for all added events, as in addHistoryOperations.
	var slabSize int
	for i := len(events) - addedCount; i < len(events); i++ {
		slabSize += proto.Size(events[i])
	}
	slab := make([]byte, 0, slabSize)
	mo := proto.MarshalOptions{}
	for i := len(events) - addedCount; i < len(events); i++ {
		off := len(slab)
		var err error
		slab, err = mo.MarshalAppend(slab, events[i])
		if err != nil {
			return err
		}

		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			//nolint:gosec
			Request: api.TransactionalUpsert{Key: getMultiEntryKeyName(keyPrefix, uint64(i)), Value: slab[off:len(slab):len(slab)]},
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

// addRawBytesStateOperations persists pre-serialized raw bytes directly,
// without re-marshaling. This preserves byte-exact determinism required for
// signature chain linking.
func addRawBytesStateOperations(req *api.TransactionalRequest, keyPrefix string, rawItems [][]byte, addedCount int, removedCount int) error {
	for i := len(rawItems) - addedCount; i < len(rawItems); i++ {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Upsert,
			//nolint:gosec
			Request: api.TransactionalUpsert{Key: getMultiEntryKeyName(keyPrefix, uint64(i)), Value: rawItems[i]},
		})
	}

	for i := len(rawItems); i < removedCount; i++ {
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

func loadWorkflowStateOnce(ctx context.Context, state state.Interface, actorID string, opts Options) (*State, error) {
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

	// Validate metadata bounds to prevent OOM from inflated state store values.
	for _, entry := range []struct {
		name  string
		value uint64
	}{
		{"inbox", metadata.GetInboxLength()},
		{"history", metadata.GetHistoryLength()},
		{"signatures", metadata.GetSignatureLength()},
		{"signing certificates", metadata.GetSigningCertificateLength()},
		{"external signing certificates", metadata.GetExternalSigningCertificateLength()},
	} {
		if entry.value > maxStateEntries {
			return nil, wferrors.NewVerificationError(
				fmt.Errorf("workflow '%s' metadata %s length %d exceeds maximum %d", actorID, entry.name, entry.value, maxStateEntries),
			)
		}
	}

	// Safe to convert after maxStateEntries bounds check above.
	//nolint:gosec
	inboxLen := int(metadata.GetInboxLength())
	//nolint:gosec
	historyLen := int(metadata.GetHistoryLength())
	//nolint:gosec
	signingCertLen := int(metadata.GetSigningCertificateLength())
	//nolint:gosec
	extSigningCertLen := int(metadata.GetExternalSigningCertificateLength())
	//nolint:gosec
	signatureLen := int(metadata.GetSignatureLength())

	// Load inbox, history, signing certs, external signing certs,
	// signatures, and custom status using a bulk request.
	wState := NewState(opts)
	wState.Generation = metadata.GetGeneration()
	// Capture the metadata ETag for optimistic-concurrency on the next save.
	// nil here just means the store didn't return one for this row, which is
	// also fine: the next save will fall through to a blind upsert.
	wState.metadataETag = res.ETag
	wState.metadataETagSeen = res.ETag != nil
	wState.Inbox = make([]*backend.HistoryEvent, 0, inboxLen)
	wState.History = make([]*backend.HistoryEvent, 0, historyLen)
	wState.RawHistory = make([][]byte, 0, historyLen)
	wState.SigningCertificates = make([]*backend.SigningCertificate, 0, signingCertLen)
	wState.ExternalSigningCertificates = make([]*backend.ExternalSigningCertificate, 0, extSigningCertLen)
	wState.Signatures = make([]*backend.HistorySignature, 0, signatureLen)

	totalKeys := inboxLen + historyLen + signingCertLen + extSigningCertLen + signatureLen + 5
	bulkReq := &api.GetBulkStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		Keys:      make([]string, totalKeys),
	}

	var n int

	bulkReq.Keys[n] = customStatusKey
	n++
	bulkReq.Keys[n] = propagatedHistoryKey
	n++
	bulkReq.Keys[n] = ParentNotifyKey
	n++
	bulkReq.Keys[n] = creationInputKey
	n++
	bulkReq.Keys[n] = encryptionKey
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

	for i := range metadata.GetExternalSigningCertificateLength() {
		bulkReq.Keys[n] = getMultiEntryKeyName(extSigCertKeyPrefix, i)
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

	// Recorded before any early return, so that a purge of a state that
	// fails to load still deletes the encryption row.
	wState.loaded = true
	encRow := bulkRes[encryptionKey]
	wState.encryptionPersisted = encRow.ETag != nil || len(encRow.Data) > 0

	defer func() {
		// TODO: @joshvanl: remove in v1.16 where we will no longer have legacy
		// state parsing issues.
		if rerr := recover(); rerr != nil {
			wfLogger.Warnf("Found legacy workflow state, ignoring and overwriting with new storage API: %s; %v", actorID, rerr)
		}
	}()

	// Parse responses. If metadata declares N inbox or history entries but
	// the bulk GET returns nil Data for any of them, return an error so the
	// caller can retry the load. Silently skipping was the previous
	// behavior, but under transient state-store read failures it
	// produces a workflow runtime state with truncated history; durabletask
	// then reports name=(unknown)/events=0 and the workflow strands on the
	// next save, which clobbers the metadata HistoryLength.
	var key string
	for i := range metadata.GetInboxLength() {
		key = getMultiEntryKeyName(inboxKeyPrefix, i)
		if bulkRes[key].Data == nil {
			return nil, &transientKeyMismatchError{
				err:  fmt.Errorf("workflow '%s': inbox key '%s' declared in metadata (inboxLength=%d) but missing from state store (transient store read failure or partial save?)", actorID, key, metadata.GetInboxLength()),
				etag: res.ETag,
			}
		}

		var hist backend.HistoryEvent
		err = proto.Unmarshal(bulkRes[key].Data, &hist)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from inbox state key '%s': %w", key, err)
		}

		wState.Inbox = append(wState.Inbox, &hist)
	}

	for i := range metadata.GetHistoryLength() {
		key = getMultiEntryKeyName(historyKeyPrefix, i)
		if bulkRes[key].Data == nil {
			return nil, &transientKeyMismatchError{
				err:  fmt.Errorf("workflow '%s': history key '%s' declared in metadata (historyLength=%d) but missing from state store (transient store read failure or partial save?)", actorID, key, metadata.GetHistoryLength()),
				etag: res.ETag,
			}
		}

		var hist backend.HistoryEvent
		err = proto.Unmarshal(bulkRes[key].Data, &hist)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history event from history state key '%s': %w", key, err)
		}

		wState.History = append(wState.History, &hist)
		wState.RawHistory = append(wState.RawHistory, bulkRes[key].Data)
	}

	for i := range metadata.GetSigningCertificateLength() {
		key = getMultiEntryKeyName(sigcertKeyPrefix, i)
		if bulkRes[key].Data == nil {
			if hasTamperMarker(wState) {
				return wState, nil
			}
			return wState, wferrors.NewVerificationError(
				fmt.Errorf("signing certificate state key '%s' declared in metadata but not found in state store — possible tampering", key),
			)
		}

		var cert backend.SigningCertificate
		err = proto.Unmarshal(bulkRes[key].Data, &cert)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal signing certificate from state key '%s': %w", key, err)
		}

		wState.SigningCertificates = append(wState.SigningCertificates, &cert)
	}

	loadedExtCerts := make([]*backend.ExternalSigningCertificate, 0, extSigningCertLen)
	for i := range metadata.GetExternalSigningCertificateLength() {
		key = getMultiEntryKeyName(extSigCertKeyPrefix, i)
		if bulkRes[key].Data == nil {
			if hasTamperMarker(wState) {
				return wState, nil
			}
			return wState, wferrors.NewVerificationError(
				fmt.Errorf("external signing certificate state key '%s' declared in metadata but not found in state store - possible tampering", key),
			)
		}

		var ext backend.ExternalSigningCertificate
		err = proto.Unmarshal(bulkRes[key].Data, &ext)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal external signing certificate from state key '%s': %w", key, err)
		}
		loadedExtCerts = append(loadedExtCerts, &ext)
	}
	if err = wState.setLoadedExternalCerts(loadedExtCerts); err != nil {
		if hasTamperMarker(wState) {
			return wState, nil
		}
		return wState, wferrors.NewVerificationError(err)
	}

	for i := range metadata.GetSignatureLength() {
		key = getMultiEntryKeyName(signatureKeyPrefix, i)
		if bulkRes[key].Data == nil {
			if hasTamperMarker(wState) {
				return wState, nil
			}
			return wState, wferrors.NewVerificationError(
				fmt.Errorf("signature state key '%s' declared in metadata but not found in state store — possible tampering", key),
			)
		}

		var sig backend.HistorySignature
		err = proto.Unmarshal(bulkRes[key].Data, &sig)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal history signature from state key '%s': %w", key, err)
		}

		wState.Signatures = append(wState.Signatures, &sig)
		raw := make([]byte, len(bulkRes[key].Data))
		copy(raw, bulkRes[key].Data)
		wState.RawSignatures = append(wState.RawSignatures, raw)
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
			jerr := json.Unmarshal(bulkRes[customStatusKey].Data, &customStatusValue)
			if jerr != nil {
				return nil, fmt.Errorf("failed to unmarshal custom status key entry: %w", err)
			}

			wState.CustomStatus.Value = customStatusValue
		}
	}

	if bulkRes[propagatedHistoryKey].ETag != nil {
		wState.propagatedHistoryPersisted = true
	}
	if len(bulkRes[propagatedHistoryKey].Data) > 0 {
		var ph protos.PropagatedHistory
		if err = proto.Unmarshal(bulkRes[propagatedHistoryKey].Data, &ph); err != nil {
			return nil, fmt.Errorf("failed to unmarshal propagated history for '%s': %w", actorID, err)
		}
		wState.IncomingHistory = &ph
	}
	if pn := bulkRes[ParentNotifyKey]; pn.ETag != nil || len(pn.Data) > 0 {
		wState.ParentNotifyPending = true
		wState.parentNotifyPersisted = true
	}
	if ci := bulkRes[creationInputKey]; ci.ETag != nil || len(ci.Data) > 0 {
		wState.creationInputPersisted = true
		var row creationInputRow
		if err = json.Unmarshal(ci.Data, &row); err == nil {
			wState.CreationInput = wrapperspb.String(row.Input)
			wState.creationInputParent = row.creationParent
		} else {
			// An unstamped row written as a bare StringValue by the first
			// binary that kept the input: adopt it for the current creation
			// and let the next save rewrite it stamped.
			var legacy wrapperspb.StringValue
			if perr := proto.Unmarshal(ci.Data, &legacy); perr != nil {
				return nil, fmt.Errorf("failed to unmarshal creation input for '%s': %w", actorID, err)
			}
			parent := executionStartedOf(wState.History, wState.Inbox).GetParentInstance()
			if parent == nil {
				wState.ClearCreationInput()
			} else {
				wState.setCreationInput(&legacy, parent)
			}
		}
	}

	wfLogger.Debugf("%s: loaded %d state records in %v", actorID, 1+len(bulkRes), time.Since(loadStartTime))

	// A workflow that was previously detected as tampered carries an unsigned
	// ExecutionCompleted(FAILED) marker as its last history event (see
	// [MarkAsTamperFailed]). It must always remain loadable so callers can observe the
	// FAILED status. Short-circuit every signing-enforcement check
	// (configuration mismatch, signature chain failure) when the marker is
	// present.
	if hasTamperMarker(wState) {
		return wState, nil
	}

	// Signing enforcement. Once signing is enabled for a cluster, it is a
	// one-way commitment: workflows created with signing must always run on
	// signing-enabled hosts, and workflows created without signing cannot be
	// migrated to signing-enabled hosts. Workflows that violate these rules are
	// terminated. Operators must ensure all unsigned workflows complete or are
	// purged before enabling signing cluster-wide.
	if len(wState.Signatures) > 0 {
		if opts.Signer == nil {
			return wState, wferrors.NewConfigurationError(
				fmt.Errorf("workflow '%s' has signed history but no signer is configured; cannot verify signatures — signing cannot be disabled for workflows that were created with signing enabled", actorID),
			)
		}
		if opts.AppID == "" {
			return wState, wferrors.NewConfigurationError(
				fmt.Errorf("workflow '%s' has signed history but app ID is not configured; cannot verify signer identity", actorID),
			)
		}
		if opts.Namespace == "" {
			return wState, wferrors.NewConfigurationError(
				fmt.Errorf("workflow '%s' has signed history but namespace is not configured; cannot verify signer identity", actorID),
			)
		}
		if err := verifySignatureChain(wState, opts.Signer); err != nil {
			return wState, wferrors.NewVerificationError(
				fmt.Errorf("workflow history signature verification failed for '%s': %w", actorID, err),
			)
		}
	} else if opts.Signer != nil && len(wState.History) > 0 {
		return wState, wferrors.NewConfigurationError(
			fmt.Errorf("workflow '%s' has %d unsigned history events but signing is enabled — signing cannot be enabled for workflows that were created without signing; ensure all unsigned workflows complete or are purged before enabling signing", actorID, len(wState.History)),
		)
	}

	// Payloads are unsealed after the signature chain verified, because the
	// signatures cover the stored (sealed) bytes.
	if len(encRow.Data) > 0 {
		if err := openEncryptedState(ctx, wState, encRow.Data, actorID, opts); err != nil {
			return wState, err
		}
	}

	return wState, nil
}

// openEncryptedState unwraps an encrypted instance's key and unseals its
// payloads. Payload encryption cannot be turned off for an instance created
// with it. A key that cannot be unwrapped (key vault unavailable, key
// missing) is a plain error, so the caller retries; a payload that does not
// unseal under the instance's key was modified, which is a verification
// error.
func openEncryptedState(ctx context.Context, s *State, row []byte, actorID string, opts Options) error {
	if opts.Encryptor == nil {
		return wferrors.NewConfigurationError(
			fmt.Errorf("workflow '%s' has encrypted payloads but workflow payload encryption is not configured; encryption cannot be disabled for workflows that were created with it", actorID),
		)
	}
	var w internalsv1pb.WorkflowEncryptionKey
	if err := proto.Unmarshal(row, &w); err != nil {
		return wferrors.NewVerificationError(fmt.Errorf("workflow '%s': malformed encryption key row: %w", actorID, err))
	}
	k, err := opts.Encryptor.Open(ctx, &w)
	if err != nil {
		return fmt.Errorf("workflow '%s': failed to open payload encryption key: %w", actorID, err)
	}
	s.EncryptionKey = k
	s.aadBase = s.workflowActorType + "||" + actorID
	s.historySealedFrom = w.GetHistorySealedFrom()
	s.encryptionRewrap = k.NeedsRewrap(opts.Encryptor.CurrentKID())
	if err := s.unsealAll(); err != nil {
		return wferrors.NewVerificationError(fmt.Errorf("workflow '%s': encrypted payload verification failed: %w", actorID, err))
	}
	return nil
}

// IsTamperMarker reports whether e is the well-known terminal event written
// by [MarkAsTamperFailed] to record that the workflow's persisted state was
// detected as tampered. It is identified by an ExecutionCompleted with
// status FAILED and FailureDetails.ErrorType set to
// [wferrors.ErrorTypeHistoryTampered]. Loaders use this check to bypass
// signature verification on workflows that have already been terminally
// failed by tamper detection — without the bypass, the broken signature
// chain would block every subsequent load.
func IsTamperMarker(e *backend.HistoryEvent) bool {
	if e == nil {
		return false
	}
	ec := e.GetExecutionCompleted()
	if ec == nil {
		return false
	}
	if ec.GetWorkflowStatus() != protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED {
		return false
	}
	fd := ec.GetFailureDetails()
	return fd != nil && fd.GetErrorType() == wferrors.ErrorTypeHistoryTampered
}

// hasTamperMarker reports whether the loaded state's history ends in the
// terminal tamper marker event written by [MarkAsTamperFailed]. Used by
// LoadWorkflowState to short-circuit verification failures on workflows
// that are already terminally failed.
func hasTamperMarker(s *State) bool {
	return s != nil && len(s.History) > 0 && IsTamperMarker(s.History[len(s.History)-1])
}

// IsCompleted reports whether the workflow's history ends in any
// ExecutionCompleted event - the runtime's terminal marker. Operates on
// stored state directly so callers that have a *State but not yet a
// runtime state (e.g. mid-load decisions) can check terminality.
func (s *State) IsCompleted() bool {
	return s != nil && len(s.History) > 0 && s.History[len(s.History)-1].GetExecutionCompleted() != nil
}

// HasTamperMarker reports whether the workflow has been terminally failed by
// tamper detection (cold-store load tamper or attestation verification
// failure). True implies IsCompleted; the converse does not hold (a normally
// completed workflow returns false).
func (s *State) HasTamperMarker() bool {
	return hasTamperMarker(s)
}

// MarkAsTamperFailed appends a single terminal ExecutionCompleted(FAILED) event to
// the workflow's history to record that its persisted state was detected as
// tampered. The original (untrusted) history, inbox, signatures, and certs
// are left intact for forensics — only the marker event is added, and it is
// not signed. Subsequent loads detect the marker via [IsTamperMarker] and
// bypass signature verification, so the workflow surfaces as terminally
// FAILED with [wferrors.ErrorTypeHistoryTampered] in its FailureDetails.
//
// MarkAsTamperFailed is idempotent: if prior already ends in a tamper marker the
// state is returned unchanged with no store write.
func MarkAsTamperFailed(ctx context.Context, astate state.Interface, actorID string, opts Options, prior *State, cause error) (*State, error) {
	s := prior
	if s == nil {
		s = NewState(opts)
	}

	if len(s.History) > 0 && IsTamperMarker(s.History[len(s.History)-1]) {
		return s, nil
	}

	s.AddToHistory(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    wferrors.ErrorTypeHistoryTampered,
					ErrorMessage: cause.Error(),
				},
			},
		},
	})

	req, err := s.GetSaveRequest(actorID)
	if err != nil {
		return nil, err
	}

	if err := astate.TransactionalStateOperation(ctx, true, req, false); err != nil {
		return nil, err
	}

	s.ResetChangeTracking()

	// Refresh the metadata ETag so a subsequent save by the caller (e.g. an
	// addWorkflowEvent that landed on this same actor right after we
	// tombstoned) doesn't trip optimistic-concurrency on the stale token we
	// loaded with. The save above bumped postgres's row version; our
	// in-memory copy is now out of date. A read failure here is non-fatal:
	// the cached ETag is cleared and the next save is refused until the
	// state is reloaded with a current one.
	metaRes, metaErr := astate.Get(ctx, &api.GetStateRequest{
		ActorType: opts.WorkflowActorType,
		ActorID:   actorID,
		Key:       MetadataKey,
	}, false)
	if metaErr != nil || metaRes == nil {
		// Unknown version: the next save is refused by GetSaveRequest until
		// the state is reloaded, rather than blind-upserted.
		// Blind upsert stays acceptable for a tombstoned, terminal instance.
		s.metadataETag = nil
		s.metadataETagSeen = false
	} else {
		s.SetMetadataETag(metaRes.ETag)
	}
	return s, nil
}

func verifySignatureChain(s *State, sgn *signer.Signer) error {
	rawEvents := s.RawHistory
	if len(s.Signatures) > 0 {
		last := s.Signatures[len(s.Signatures)-1]
		coveredEvents := last.GetStartEventIndex() + last.GetEventCount()
		if coveredEvents != uint64(len(rawEvents)) {
			// Signature coverage does not match history length. Either an unsigned
			// gap exists (coveredEvents < len) because the workflow ran on a
			// non-signing host, or events were removed from history while
			// signatures remain (coveredEvents > len). In both cases, the state
			// has no valid integrity proof and the workflow cannot be recovered.
			return fmt.Errorf("signatures cover events [0, %d) but %d history events exist — signature coverage mismatch, state cannot be verified",
				coveredEvents, len(rawEvents))
		}
	}
	if _, err := historysigning.VerifyChain(historysigning.VerifyChainOptions{
		RawSignatures: s.RawSignatures,
		Certs:         s.SigningCertificates,
		AllRawEvents:  rawEvents,
		Signer:        sgn,
	}); err != nil {
		return err
	}

	// Verify that all signing certificates belong to the expected app and
	// namespace. This prevents cross-app signature forgery where App B signs
	// events for App A's workflow using a valid certificate from the same
	// trust domain, and cross-namespace forgery where the same app-id in a
	// different namespace forges signatures. AppID and Namespace are
	// guaranteed non-empty here because LoadWorkflowState rejects signed
	// workflows with empty values.
	for i, cert := range s.SigningCertificates {
		if err := verifyCertAppIdentity(cert.GetCertificate(), s.appID, s.namespace); err != nil {
			return fmt.Errorf("signing certificate %d: %w", i, err)
		}
	}

	return nil
}

// verifyCertAppIdentity checks that a DER-encoded signing certificate chain
// contains a SPIFFE ID matching the expected app ID and namespace. SPIFFE
// IDs follow the pattern: spiffe://<trust-domain>/ns/<namespace>/<app-id>
// The function validates the full path structure, not just the last segment.
func verifyCertAppIdentity(certChainDER []byte, expectedAppID, expectedNamespace string) error {
	if expectedAppID == "" {
		return errors.New("expectedAppID must not be empty")
	}
	if expectedNamespace == "" {
		return errors.New("expectedNamespace must not be empty")
	}
	if len(certChainDER) == 0 {
		return errors.New("certificate chain is empty")
	}

	certs, err := x509.ParseCertificates(certChainDER)
	if err != nil {
		return fmt.Errorf("failed to parse certificate chain: %w", err)
	}
	if len(certs) == 0 {
		return errors.New("no certificates in chain")
	}

	leaf := certs[0]
	spiffeID, err := x509svid.IDFromCert(leaf)
	if err != nil {
		return fmt.Errorf("failed to extract SPIFFE ID: %w", err)
	}

	// Validate the full SPIFFE path structure: /ns/<namespace>/<app-id>
	// Split produces: ["", "ns", "<namespace>", "<app-id>"]
	segments := strings.Split(spiffeID.Path(), "/")
	if len(segments) != 4 || segments[0] != "" || segments[1] != "ns" || segments[2] == "" || segments[3] == "" {
		return fmt.Errorf("SPIFFE ID %q does not match expected path format /ns/<namespace>/<app-id>", spiffeID)
	}
	certNamespace := segments[2]
	certAppID := segments[3]

	if certNamespace != expectedNamespace {
		return fmt.Errorf("certificate SPIFFE ID namespace %q does not match expected namespace %q", certNamespace, expectedNamespace)
	}
	if certAppID != expectedAppID {
		return fmt.Errorf("certificate SPIFFE ID app %q does not match expected app %q", certAppID, expectedAppID)
	}

	return nil
}

func (s *State) GetPurgeRequest(actorID string) (*api.TransactionalRequest, error) {
	req := &api.TransactionalRequest{
		ActorType: s.workflowActorType,
		ActorID:   actorID,
		// Initial capacity should be enough to contain the entire inbox, history, signing data, and custom status + metadata
		Operations: make([]api.TransactionalOperation, 0, len(s.Inbox)+len(s.History)+len(s.SigningCertificates)+len(s.ExternalSigningCertificates)+len(s.Signatures)+6),
	}

	addPurgeStateOperations(req, inboxKeyPrefix, len(s.Inbox))
	addPurgeStateOperations(req, historyKeyPrefix, len(s.History))
	addPurgeStateOperations(req, sigcertKeyPrefix, len(s.SigningCertificates))
	addPurgeStateOperations(req, extSigCertKeyPrefix, len(s.ExternalSigningCertificates))
	addPurgeStateOperations(req, signatureKeyPrefix, len(s.Signatures))

	// Only emit deletes for optional keys that we know are persisted.
	if s.customStatusPersisted {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: customStatusKey},
		})
	}
	if s.propagatedHistoryPersisted {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: propagatedHistoryKey},
		})
	}
	if s.parentNotifyPersisted {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: ParentNotifyKey},
		})
	}
	if s.creationInputPersisted {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: creationInputKey},
		})
	}
	if s.encryptionPersisted {
		req.Operations = append(req.Operations, api.TransactionalOperation{
			Operation: api.Delete,
			Request:   api.TransactionalDelete{Key: encryptionKey},
		})
	}

	req.Operations = append(req.Operations, api.TransactionalOperation{
		Operation: api.Delete,
		Request:   api.TransactionalDelete{Key: MetadataKey},
	})

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
	// Full rehydrate: force the next save to persist the custom status
	// regardless of what the store row currently holds.
	s.customStatusChanged = true
	s.Generation = state.GetGeneration()
}

// HistoryKey is the row that holds history event i.
func HistoryKey(i uint64) string {
	return getMultiEntryKeyName(historyKeyPrefix, i)
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
