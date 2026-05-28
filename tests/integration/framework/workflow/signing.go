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

package workflow

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/crypto/spiffe/trustanchors/fake"
)

// SigningData holds signatures, certificates, and raw history events for a
// workflow instance, loaded from the state store for verification.
type SigningData struct {
	// RawSignatures are the raw serialized bytes of each HistorySignature
	// as stored. Required for digest computation in chain verification.
	RawSignatures [][]byte
	// Signatures are the parsed HistorySignature protos.
	Signatures []*protos.HistorySignature
	// Certs are the signing certificates.
	Certs []*protos.SigningCertificate
	// RawEvents are the raw serialized bytes of each history event as stored.
	RawEvents [][]byte
}

// UnmarshalSigningData reads and unmarshals signatures, certificates, and raw
// history events from the SQLite state store for the given workflow instance.
func UnmarshalSigningData(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) SigningData {
	t.Helper()

	sigValues := db.ReadStateValues(t, ctx, instanceID, "signature")
	certValues := db.ReadStateValues(t, ctx, instanceID, "sigcert")
	rawEvents := db.ReadStateValues(t, ctx, instanceID, "history")

	rawSigs := make([][]byte, len(sigValues))
	sigs := make([]*protos.HistorySignature, len(sigValues))
	for i, v := range sigValues {
		sigs[i] = new(protos.HistorySignature)
		require.NoError(t, proto.Unmarshal(v, sigs[i]))
		rawSigs[i] = make([]byte, len(v))
		copy(rawSigs[i], v)
	}

	certs := make([]*protos.SigningCertificate, len(certValues))
	for i, v := range certValues {
		certs[i] = new(protos.SigningCertificate)
		require.NoError(t, proto.Unmarshal(v, certs[i]))
	}

	return SigningData{
		RawSignatures: rawSigs,
		Signatures:    sigs,
		Certs:         certs,
		RawEvents:     rawEvents,
	}
}

// SignatureCount returns the number of signature entries stored for the
// given workflow instance. Use this in tests to verify signing happened
// or did not happen, instead of calling CountStateKeys directly with a
// raw key prefix string (which is error-prone).
func SignatureCount(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) int {
	t.Helper()
	return len(db.ReadStateValues(t, ctx, instanceID, "signature"))
}

// CertificateCount returns the number of signing certificate entries stored
// for the given workflow instance.
func CertificateCount(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) int {
	t.Helper()
	return len(db.ReadStateValues(t, ctx, instanceID, "sigcert"))
}

// HistoryCount returns the number of history entries stored for the given
// workflow instance.
func HistoryCount(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) int {
	t.Helper()
	return len(db.ReadStateValues(t, ctx, instanceID, "history"))
}

// MutateMetadata loads the persisted BackendWorkflowStateMetadata for the
// given workflow instance, applies the mutation, and writes it back. Used
// by negative tests that simulate state store tampering.
func MutateMetadata(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string, mutate func(*backend.BackendWorkflowStateMetadata)) {
	t.Helper()

	key, raw := db.ReadStateValue(t, ctx, instanceID, "metadata")

	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(raw, &metadata))

	mutate(&metadata)

	updated, err := proto.Marshal(&metadata)
	require.NoError(t, err)
	db.WriteStateValue(t, ctx, key, updated)
}

// VerifySignatureChain verifies the full history signature chain for a
// workflow instance, including cryptographic signatures and certificate
// chain-of-trust against the given trust anchors.
func VerifySignatureChain(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string, trustAnchors []byte) {
	t.Helper()

	data := UnmarshalSigningData(t, ctx, db, instanceID)

	require.NotEmpty(t, data.RawSignatures, "expected signature records")
	require.NotEmpty(t, data.Certs, "expected certificate records")
	require.NotEmpty(t, data.RawEvents, "expected history records")

	authorities := parsePEMCertificates(t, trustAnchors)
	s := signer.New(nil, fake.New(authorities...))

	_, err := historysigning.VerifyChain(historysigning.VerifyChainOptions{
		RawSignatures: data.RawSignatures,
		Certs:         data.Certs,
		AllRawEvents:  data.RawEvents,
		Signer:        s,
	})
	require.NoError(t, err)
}

// parsePEMCertificates parses a PEM-encoded certificate bundle into x509
// certificates.
func parsePEMCertificates(t *testing.T, pemData []byte) []*x509.Certificate {
	t.Helper()
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		certs = append(certs, cert)
	}
	require.NotEmpty(t, certs, "no certificates found in PEM data")
	return certs
}

// VerifyCertAppID checks that all signing certificates for a workflow instance
// contain a SPIFFE ID matching the expected app ID in the "default" namespace,
// and that each certificate has a 2-deep chain (leaf + issuer intermediate).
func VerifyCertAppID(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID, expectedAppID string) {
	t.Helper()

	certValues := db.ReadStateValues(t, ctx, instanceID, "sigcert")
	require.NotEmpty(t, certValues, "expected certificate records")

	expectedID := spiffeid.RequireFromSegments(
		spiffeid.RequireTrustDomainFromString("public"),
		"ns", "default", expectedAppID,
	)

	for i, raw := range certValues {
		var sc protos.SigningCertificate
		require.NoError(t, proto.Unmarshal(raw, &sc))

		certs, err := x509.ParseCertificates(sc.GetCertificate())
		require.NoError(t, err, "failed to parse signing certificate %d", i)
		require.Len(t, certs, 2, "signing certificate %d should have leaf + issuer intermediate", i)
		leaf := certs[0]

		// Verify the leaf was issued by the intermediate.
		assert.Equal(t, certs[1].Subject, leaf.Issuer, "signing certificate %d leaf issuer mismatch", i)
		assert.False(t, leaf.IsCA, "signing certificate %d leaf should not be a CA", i)
		assert.True(t, certs[1].IsCA, "signing certificate %d intermediate should be a CA", i)

		require.NotEmpty(t, leaf.URIs, "signing certificate %d has no URI SANs", i)
		id, err := spiffeid.FromURI(leaf.URIs[0])
		require.NoError(t, err, "signing certificate %d has invalid SPIFFE ID", i)

		assert.Equal(t, expectedID, id, "signing certificate %d SPIFFE ID mismatch", i)
	}
}

// ForgeChunkWithSpiffePath rewrites a single PropagatedHistoryChunk so its
// signing material attests to spiffe://<sentry-trust-domain><spiffePath> -
// a cert that the test framework's Sentry would not have issued in
// production (e.g. wrong namespace) but that nevertheless chains cleanly
// to that Sentry's trust anchor.
//
// The leaf cert is freshly issued against Sentry's IssChain, and a fresh
// HistorySignature over chunk.RawEvents is produced with the matching
// private key, then written into chunk.RawSignatures. The result: the
// receiver's chain-of-trust verification and per-signature cryptographic
// verification both succeed; only the SPIFFE identity check (app or
// namespace component) can fire. This isolates the identity gate from
// the chain gate in tampering tests.
//
// Mutates chunk in place. Caller should set the modified chunk back into
// the persisted PropagatedHistory before re-loading.
func ForgeChunkWithSpiffePath(t *testing.T, sen *sentry.Sentry, chunk *protos.PropagatedHistoryChunk, spiffePath string) {
	t.Helper()
	require.NotNil(t, sen, "sentry must not be nil; spin the workflow up with WithMTLS")
	require.NotNil(t, chunk)
	require.NotEmpty(t, chunk.GetRawEvents(), "chunk must have rawEvents to re-sign")

	td := spiffeid.RequireTrustDomainFromString(sen.TrustDomain(t))
	// spiffeid.FromPath validates the path so a typo here is caught at
	// test compile time rather than misleading the assertion.
	id, err := spiffeid.FromPath(td, spiffePath)
	require.NoError(t, err)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	caBundle := sen.CABundle().X509
	require.NotEmpty(t, caBundle.IssChain, "sentry CA bundle has no issuance chain")

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "issued-by-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{id.URL()},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, template, caBundle.IssChain[0], pub, caBundle.IssKey)
	require.NoError(t, err)

	// Wire shape: leaf DER concatenated with intermediate DER (same as
	// signer.Signer.CertChainDER produces and what BuildSignedChunk persists).
	chain := make([]byte, 0, len(leafDER)+len(caBundle.IssChain[0].Raw))
	chain = append(chain, leafDER...)
	chain = append(chain, caBundle.IssChain[0].Raw...)

	// Build a fresh HistorySignature over the chunk's rawEvents, signed
	// by the leaf's private key. No previous-signature digest because
	// this is a single-batch chunk-local signature (matches how the
	// producer signs chunks at dispatch time). Bind the chunk's existing
	// instance/workflow name so the signature stays self-consistent and
	// the test fails purely on the SPIFFE identity check.
	eventsDigest := historysigning.EventsDigest(chunk.GetRawEvents())
	sigInput := historysigning.SignatureInput(nil, eventsDigest, &historysigning.PropagationContext{
		InstanceID:   chunk.GetInstanceId(),
		WorkflowName: chunk.GetWorkflowName(),
	})
	sigBytes := ed25519.Sign(priv, sigInput)

	histSig := &protos.HistorySignature{
		StartEventIndex:  0,
		EventCount:       uint64(len(chunk.GetRawEvents())),
		EventsDigest:     eventsDigest,
		CertificateIndex: 0,
		Signature:        sigBytes,
	}
	rawSig, err := proto.MarshalOptions{Deterministic: true}.Marshal(histSig)
	require.NoError(t, err)

	chunk.RawSignatures = [][]byte{rawSig}
	chunk.SigningCertChains = [][]byte{chain}
}
