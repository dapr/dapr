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

package jwks

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

const (
	// Trust domain for Sentry
	sentryTrustDomain = "localhost"
	// Namespace for sentry
	sentryNamespace = "default"
)

// Keys used to sign and verify JWTs
var (
	jwtSigningKeyPriv    jwk.Key
	jwtSigningKeyPubJSON []byte
)

func init() {
	// Generate a signing key
	privK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("failed to generate private key: %v", err)
	}
	jwtSigningKeyPriv, err = jwk.FromRaw(privK)
	if err != nil {
		log.Fatalf("failed to import private key as JWK: %v", err)
	}
	jwtSigningKeyPriv.Set("kid", "mykey")
	jwtSigningKeyPriv.Set("alg", "ES256")
	jwtSigningKeyPub, err := jwtSigningKeyPriv.PublicKey()
	if err != nil {
		log.Fatalf("failed to get public key from JWK: %v", err)
	}
	jwtSigningKeyPubJSON, err = json.Marshal(jwtSigningKeyPub)
	if err != nil {
		log.Fatalf("failed to marshal public key from JWK: %v", err)
	}
}

// Generate a CSR given a private key.
func generateCSR(id string, privKey crypto.PrivateKey) ([]byte, error) {
	csr := x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: id},
		DNSNames: []string{id},
	}
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, &csr, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create sidecar csr: %w", err)
	}

	csrPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer})
	return csrPem, nil
}

func generateJWT(sub string) *jwt.Builder {
	now := time.Now()
	return jwt.NewBuilder().
		Audience([]string{fmt.Sprintf("spiffe://%s/ns/%s/dapr-sentry", sentryTrustDomain, sentryNamespace)}).
		Expiration(now.Add(time.Hour)).
		IssuedAt(now).
		Subject(sub)
}

func signJWT(builder *jwt.Builder) ([]byte, error) {
	token, err := builder.Build()
	if err != nil {
		return nil, err
	}

	return jwt.Sign(token, jwt.WithKey(jwa.ES256, jwtSigningKeyPriv))
}

func validateCertificateResponse(t *testing.T, res *sentrypbv1.SignCertificateResponse, sentryBundle bundle.Bundle, expectSPIFFEID string) {
	t.Helper()

	require.NotEmpty(t, res.GetWorkloadCertificate())

	rest := res.GetWorkloadCertificate()

	// First block should contain the issued workload certificate
	{
		var block *pem.Block
		block, rest = pem.Decode(rest)
		require.NotEmpty(t, block)
		require.Equal(t, "CERTIFICATE", block.Type)

		cert, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)

		certURIs := make([]string, len(cert.URIs))
		for i, v := range cert.URIs {
			certURIs[i] = v.String()
		}
		assert.Equal(t, []string{expectSPIFFEID}, certURIs)
		assert.Empty(t, cert.DNSNames)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	}

	// Second block should contain the Sentry CA certificate
	{
		var block *pem.Block
		block, rest = pem.Decode(rest)
		require.Empty(t, rest)
		require.NotEmpty(t, block)
		require.Equal(t, "CERTIFICATE", block.Type)

		cert, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)

		assert.Empty(t, cert.DNSNames)
	}
}

func generateCACertificate(t *testing.T) (caCert []byte, caKey []byte) {
	// Generate a private key for the CA
	caPrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Create a self-signed CA certificate
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CA"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertificateDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPrivateKey.Public(), caPrivateKey)
	require.NoError(t, err)
	caCert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertificateDER})

	caPrivateKeyDER, err := x509.MarshalPKCS8PrivateKey(caPrivateKey)
	require.NoError(t, err)
	caKey = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caPrivateKeyDER})

	return caCert, caKey
}

func generateTLSCertificates(t *testing.T, caCert []byte, caKey []byte, dnsName string) (cert []byte, key []byte) {
	// Load the CA certificate and key
	caCertBlock, _ := pem.Decode(caCert)
	caCertificate, err := x509.ParseCertificate(caCertBlock.Bytes)
	require.NoError(t, err)

	caKeyBlock, _ := pem.Decode(caKey)
	caPrivateKey, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	require.NoError(t, err)

	// Generate a private key for the new cert
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Create a certificate
	certData := &x509.Certificate{
		SerialNumber: big.NewInt(10),
		Subject:      pkix.Name{Organization: []string{"localhost"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{dnsName},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, certData, caCertificate, privKey.Public(), caPrivateKey)
	require.NoError(t, err)

	cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	require.NoError(t, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(privKey)
	require.NoError(t, err)
	key = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	return cert, key
}
