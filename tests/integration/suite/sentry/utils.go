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

package metadata

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const (
	// Trust domain for Sentry
	sentryTrustDomain = "localhost"
	// Namespace for sentry
	sentryNamespace = "default"
)

// Keys used to sign and verify JWTs
const (
	jwtSigningKeyPubJSON  = `{"kid":"mykey","kty":"EC","crv":"P-256","x":"UMn1c2ioMNi2DqvC8hdBVUERFZ97eVFsNVcQIgR0Hso","y":"uT1a0P3UOLiObve2-pOMFx2BVzLz5rFtU-qmQBPWwd0"}`
	jwtSigningKeyPrivJSON = `{"kid":"mykey","kty":"EC","crv":"P-256","d":"5wV7hDpqt1L3uaXa1Xj7X3ieaV9A-Hyj2Kv-qxpwSjM","x":"UMn1c2ioMNi2DqvC8hdBVUERFZ97eVFsNVcQIgR0Hso","y":"uT1a0P3UOLiObve2-pOMFx2BVzLz5rFtU-qmQBPWwd0"}`
)

var jwtSigningKeyPriv jwk.Key

func init() {
	jwtSigningKeyPriv, _ = jwk.ParseKey([]byte(jwtSigningKeyPrivJSON))
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
