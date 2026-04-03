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
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend/historysigning"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/crypto/spiffe/trustanchors/fake"
)

// UnmarshalSigningData reads and unmarshals signatures, certificates, and raw
// history events from the SQLite state store for the given workflow instance.
func UnmarshalSigningData(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string) ([]*protos.HistorySignature, []*protos.SigningCertificate, [][]byte) {
	t.Helper()

	sigValues := db.ReadStateValues(t, ctx, instanceID, "signature")
	certValues := db.ReadStateValues(t, ctx, instanceID, "sigcert")
	rawEvents := db.ReadStateValues(t, ctx, instanceID, "history")

	sigs := make([]*protos.HistorySignature, len(sigValues))
	for i, v := range sigValues {
		sigs[i] = new(protos.HistorySignature)
		require.NoError(t, proto.Unmarshal(v, sigs[i]))
	}

	certs := make([]*protos.SigningCertificate, len(certValues))
	for i, v := range certValues {
		certs[i] = new(protos.SigningCertificate)
		require.NoError(t, proto.Unmarshal(v, certs[i]))
	}

	return sigs, certs, rawEvents
}

// VerifySignatureChain verifies the full history signature chain for a
// workflow instance, including cryptographic signatures and certificate
// chain-of-trust against the given trust anchors.
func VerifySignatureChain(t *testing.T, ctx context.Context, db *sqlite.SQLite, instanceID string, trustAnchors []byte) {
	t.Helper()

	sigs, certs, rawEvents := UnmarshalSigningData(t, ctx, db, instanceID)

	require.NotEmpty(t, sigs, "expected signature records")
	require.NotEmpty(t, certs, "expected certificate records")
	require.NotEmpty(t, rawEvents, "expected history records")

	authorities := parsePEMCertificates(t, trustAnchors)
	s := signer.New(nil, fake.New(authorities...))

	require.NoError(t, historysigning.VerifyChain(historysigning.VerifyChainOptions{
		Signatures:   sigs,
		Certs:        certs,
		AllRawEvents: rawEvents,
		Signer:       s,
	}))
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
