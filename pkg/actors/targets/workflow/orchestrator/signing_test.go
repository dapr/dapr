/*
Copyright 2026 The Dapr Authors
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

package orchestrator

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/actors/api"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/crypto/spiffe/trustanchors/fake"
)

func generateTestCert(t *testing.T) ([]byte, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		URIs:         []*url.URL{{Scheme: "spiffe", Host: "example.org", Path: "/ns/default/app-a"}},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	require.NoError(t, err)
	return certDER, priv
}

func testSigner(t *testing.T, certDER []byte, key ed25519.PrivateKey) *signer.Signer {
	t.Helper()
	return testSignerWithTrust(t, certDER, key, false)
}

func testSignerWithTrust(t *testing.T, certDER []byte, key ed25519.PrivateKey, withTrustAnchors bool) *signer.Signer {
	t.Helper()
	certs, err := x509.ParseCertificates(certDER)
	require.NoError(t, err)
	id, err := x509svid.IDFromCert(certs[0])
	require.NoError(t, err)
	source := &staticSVIDSource{svid: &x509svid.SVID{
		ID:           id,
		Certificates: certs,
		PrivateKey:   key,
	}}
	if withTrustAnchors {
		return signer.New(source, fake.New(certs...))
	}
	return signer.New(source, nil)
}

type staticSVIDSource struct {
	svid *x509svid.SVID
}

func (s *staticSVIDSource) GetX509SVID() (*x509svid.SVID, error) {
	return s.svid, nil
}

func testState(events ...*backend.HistoryEvent) *wfenginestate.State {
	s := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "test-app",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	for _, e := range events {
		s.AddToHistory(e)
	}
	return s
}

func testHistoryEvent(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.New(time.Date(2026, 3, 18, 12, 0, int(id), 0, time.UTC)),
		EventType: &protos.HistoryEvent_WorkflowStarted{
			WorkflowStarted: &protos.WorkflowStartedEvent{},
		},
	}
}

func TestSignNewEvents_NilCrypto(t *testing.T) {
	t.Parallel()

	o := &orchestrator{factory: &factory{signer: nil}}
	s := testState(testHistoryEvent(0))

	err := o.signNewEvents(s, 1)
	require.NoError(t, err)

	assert.Empty(t, s.Signatures)
	assert.Empty(t, s.SigningCertificates)
}

func TestSignNewEvents_ZeroEvents(t *testing.T) {
	t.Parallel()

	certDER, priv := generateTestCert(t)
	o := &orchestrator{factory: &factory{signer: testSigner(t, certDER, priv)}}
	s := testState(testHistoryEvent(0))

	err := o.signNewEvents(s, 0)
	require.NoError(t, err)

	assert.Empty(t, s.Signatures)
	assert.Empty(t, s.SigningCertificates)
}

func TestSignNewEvents_SignsAndAppends(t *testing.T) {
	t.Parallel()

	certDER, priv := generateTestCert(t)
	o := &orchestrator{factory: &factory{signer: testSigner(t, certDER, priv)}}

	events := []*backend.HistoryEvent{
		testHistoryEvent(0),
		testHistoryEvent(1),
		testHistoryEvent(2),
	}
	s := testState(events...)

	err := o.signNewEvents(s, 3)
	require.NoError(t, err)

	assert.Len(t, s.Signatures, 1)
	assert.Len(t, s.SigningCertificates, 1)

	sig := s.Signatures[0]
	assert.Equal(t, uint64(0), sig.GetStartEventIndex())
	assert.Equal(t, uint64(3), sig.GetEventCount())
	assert.Nil(t, sig.GetPreviousSignatureDigest())
}

func TestSignNewEvents_ChainsToExistingSignature(t *testing.T) {
	t.Parallel()

	certDER, priv := generateTestCert(t)
	o := &orchestrator{factory: &factory{signer: testSigner(t, certDER, priv)}}

	// First batch: sign 2 events.
	events := []*backend.HistoryEvent{
		testHistoryEvent(0),
		testHistoryEvent(1),
	}
	s := testState(events...)

	err := o.signNewEvents(s, 2)
	require.NoError(t, err)
	require.Len(t, s.Signatures, 1)
	require.Len(t, s.SigningCertificates, 1)

	// Second batch: add and sign 1 more event.
	s.AddToHistory(testHistoryEvent(2))

	err = o.signNewEvents(s, 1)
	require.NoError(t, err)

	require.Len(t, s.Signatures, 2)
	// Certificate should be reused.
	assert.Len(t, s.SigningCertificates, 1)

	sig2 := s.Signatures[1]
	assert.Equal(t, uint64(2), sig2.GetStartEventIndex())
	assert.Equal(t, uint64(1), sig2.GetEventCount())
	assert.NotNil(t, sig2.GetPreviousSignatureDigest())
}

func TestSignNewEvents_SetsMarshaledNewHistory(t *testing.T) {
	t.Parallel()

	certDER, priv := generateTestCert(t)
	o := &orchestrator{factory: &factory{signer: testSigner(t, certDER, priv)}}

	events := []*backend.HistoryEvent{
		testHistoryEvent(0),
		testHistoryEvent(1),
	}
	s := testState(events...)

	err := o.signNewEvents(s, 2)
	require.NoError(t, err)

	// Verify the marshaled bytes were set by checking GetSaveRequest produces
	// upserts for history keys with the correct deterministic bytes.
	req, err := s.GetSaveRequest("test")
	require.NoError(t, err)

	for i, e := range events {
		expected, merr := historysigning.MarshalEvent(e)
		require.NoError(t, merr)

		key := fmt.Sprintf("history-%06d", i)
		found := false
		for _, op := range req.Operations {
			if op.Operation == api.Upsert {
				if u, ok := op.Request.(api.TransactionalUpsert); ok {
					if u.Key == key {
						assert.Equal(t, expected, u.Value, "history event %d bytes should match deterministic marshal", i)
						found = true
						break
					}
				}
			}
		}
		assert.True(t, found, "expected upsert for %s", key)
	}
}

func TestSignNewEvents_VerifiesWithHistorySigning(t *testing.T) {
	t.Parallel()

	certDER, priv := generateTestCert(t)
	sig := testSigner(t, certDER, priv)
	o := &orchestrator{factory: &factory{signer: sig}}

	events := []*backend.HistoryEvent{
		testHistoryEvent(0),
		testHistoryEvent(1),
		testHistoryEvent(2),
	}
	st := testState(events...)

	err := o.signNewEvents(st, 3)
	require.NoError(t, err)

	// Independently verify the signature using historysigning.VerifySignature.
	rawEvents := make([][]byte, len(events))
	for i, e := range events {
		rawEvents[i], err = historysigning.MarshalEvent(e)
		require.NoError(t, err)
	}

	err = historysigning.VerifySignature(sig, st.Signatures[0], st.SigningCertificates, rawEvents)
	require.NoError(t, err)
}

// TestSignNewEvents_RoundTripDeterminism verifies that signatures survive a
// full sign → save → load → verify cycle. The saved raw bytes must be
// preserved exactly so that chain linking digests remain valid.
func TestSignNewEvents_RoundTripDeterminism(t *testing.T) {
	t.Parallel()

	certDER, priv := generateTestCert(t)
	sgn := testSignerWithTrust(t, certDER, priv, true)
	o := &orchestrator{factory: &factory{signer: sgn}}

	// Sign two batches to test chain linking across a save cycle.
	events := []*backend.HistoryEvent{
		testHistoryEvent(0),
		testHistoryEvent(1),
	}
	st := testState(events...)

	err := o.signNewEvents(st, 2)
	require.NoError(t, err)
	require.Len(t, st.Signatures, 1)
	require.Len(t, st.RawSignatures, 1)

	// Build save request — this is what goes to the state store.
	req, err := st.GetSaveRequest("test-actor")
	require.NoError(t, err)

	// Extract the persisted bytes for signatures, certs, and history.
	savedBytes := make(map[string][]byte)
	for _, op := range req.Operations {
		if op.Operation == api.Upsert {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				if v, ok := u.Value.([]byte); ok {
					savedBytes[u.Key] = v
				}
			}
		}
	}

	// Verify signature bytes were saved.
	sigBytes, ok := savedBytes["signature-000000"]
	require.True(t, ok, "signature-000000 should be in save request")
	assert.Equal(t, st.RawSignatures[0], sigBytes,
		"saved bytes must match RawSignatures exactly")

	// Simulate a load: unmarshal signature from saved bytes and verify chain.
	var loadedSig backend.HistorySignature
	require.NoError(t, proto.Unmarshal(sigBytes, &loadedSig))

	// Reconstruct raw events from saved history bytes.
	var rawEvents [][]byte
	for i := 0; ; i++ {
		key := fmt.Sprintf("history-%06d", i)
		b, exists := savedBytes[key]
		if !exists {
			break
		}
		rawEvents = append(rawEvents, b)
	}
	require.Len(t, rawEvents, 2)

	// Verify chain using loaded raw bytes (same as LoadWorkflowState would).
	err = historysigning.VerifyChain(historysigning.VerifyChainOptions{
		RawSignatures: [][]byte{sigBytes},
		Certs:         st.SigningCertificates,
		AllRawEvents:  rawEvents,
		Signer:        sgn,
	})
	require.NoError(t, err, "signatures must verify after round-trip")

	// Now add a second batch and verify chain linking works with the
	// raw bytes from the first batch. Simulate what LoadWorkflowState
	// does: preserve RawSignatures across the save cycle.
	savedRawSigs := make([][]byte, len(st.RawSignatures))
	copy(savedRawSigs, st.RawSignatures)
	st.ResetChangeTracking()
	// Simulate reload: restore raw signatures as LoadWorkflowState would.
	st.RawSignatures = savedRawSigs
	st.AddToHistory(testHistoryEvent(2))

	err = o.signNewEvents(st, 1)
	require.NoError(t, err)
	require.Len(t, st.Signatures, 2)
	require.Len(t, st.RawSignatures, 2)

	// Rebuild raw events including the new one.
	newRaw, err := historysigning.MarshalEvent(testHistoryEvent(2))
	require.NoError(t, err)
	rawEvents = append(rawEvents, newRaw)

	// Verify full chain with both signatures.
	err = historysigning.VerifyChain(historysigning.VerifyChainOptions{
		RawSignatures: st.RawSignatures,
		Certs:         st.SigningCertificates,
		AllRawEvents:  rawEvents,
		Signer:        sgn,
	})
	require.NoError(t, err, "chained signatures must verify after round-trip")
}
