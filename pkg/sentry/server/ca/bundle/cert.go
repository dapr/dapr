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

package bundle

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry.bundle.Bundle")

const (
	// defaultCATTL is the default CA certificate TTL.
	defaultCATTL = 365 * 24 * time.Hour
)

// serialNumber returns the serial number of the certificate.
func serialNumber() (*big.Int, error) {
	serialNumLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNum, err := rand.Int(rand.Reader, serialNumLimit)
	if err != nil {
		return nil, fmt.Errorf("error generating serial number: %w", err)
	}
	return serialNum, nil
}

// generateBaseCert returns a base non-CA cert that can be made a workload or CA cert
// By adding subjects, key usage and additional properties.
func generateBaseCert(ttl, skew time.Duration) (*x509.Certificate, error) {
	serNum, err := serialNumber()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	// Allow for clock skew with the NotBefore validity bound.
	notBefore := now.Add(-1 * skew)
	notAfter := now.Add(ttl)

	return &x509.Certificate{
		SerialNumber: serNum,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}, nil
}

// generateRootCert returns a CA root x509 Certificate.
func generateRootCert(trustDomain string, skew time.Duration, overrideTTL *time.Duration) (*x509.Certificate, error) {
	ttl := defaultCATTL
	if overrideTTL != nil {
		ttl = *overrideTTL
	}
	cert, err := generateBaseCert(ttl, skew)
	if err != nil {
		return nil, err
	}

	cert.KeyUsage |= x509.KeyUsageCertSign
	cert.Subject = pkix.Name{Organization: []string{trustDomain}}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256
	return cert, nil
}

// generateIssuerCert returns a CA issuing x509 Certificate.
func generateIssuerCert(trustDomain string, skew time.Duration, overrideTTL *time.Duration) (*x509.Certificate, error) {
	ttl := defaultCATTL
	if overrideTTL != nil {
		ttl = *overrideTTL
	}
	cert, err := generateBaseCert(ttl, skew)
	if err != nil {
		return nil, err
	}

	td, err := spiffeid.TrustDomainFromString(trustDomain)
	if err != nil {
		return nil, err
	}

	sentryID, err := spiffe.FromStrings(td, security.CurrentNamespace(), "dapr-sentry")
	if err != nil {
		return nil, fmt.Errorf("failed to generate sentry ID: %w", err)
	}

	cert.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	cert.Subject = pkix.Name{Organization: []string{sentryID.URL().String()}}
	cert.IsCA = true
	cert.BasicConstraintsValid = true
	cert.SignatureAlgorithm = x509.ECDSAWithSHA256

	return cert, nil
}

// GenerateWorkloadCert returns a CA issuing x509 Certificate.
func GenerateWorkloadCert(sig x509.SignatureAlgorithm, ttl, skew time.Duration, id *spiffe.Parsed) (*x509.Certificate, error) {
	cert, err := generateBaseCert(ttl, skew)
	if err != nil {
		return nil, err
	}

	cert.ExtKeyUsage = append(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth)
	cert.SignatureAlgorithm = sig
	cert.URIs = []*url.URL{id.URL()}

	return cert, nil
}
