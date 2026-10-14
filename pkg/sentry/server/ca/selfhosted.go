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

package ca

import (
	"context"
	"fmt"
	"os"

	"github.com/lestrrat-go/jwx/v2/jwk"

	"github.com/dapr/dapr/pkg/sentry/config"
	bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

// selfhosted is a store that uses the file system as the secret store.
type selfhosted struct {
	config config.Config
}

// store saves the certificate bundle to the local filesystem.
func (s *selfhosted) store(_ context.Context, bndl bundle.Bundle) error {
	type fileWrite struct {
		path string
		data []byte
	}

	// Order matters for crash safety during CA renewal: the appended trust
	// anchors are persisted before the pending issuer pair so a torn write
	// never leaves a pending issuer which does not chain to a stored anchor.
	writes := []fileWrite{
		{s.config.RootCertPath, bndl.X509.TrustAnchors},
		{s.config.NextIssuerKeyPath, bndl.X509.NextIssKeyPEM},
		{s.config.NextIssuerCertPath, bndl.X509.NextIssChainPEM},
		{s.config.IssuerCertPath, bndl.X509.IssChainPEM},
		{s.config.IssuerKeyPath, bndl.X509.IssKeyPEM},
	}

	if s.config.JWT.Enabled && bndl.JWT != nil {
		writes = append(writes,
			fileWrite{s.config.JWT.SigningKeyPath, bndl.JWT.SigningKeyPEM},
			fileWrite{s.config.JWT.JWKSPath, bndl.JWT.JWKSJson},
		)
	}

	// Write each file if the path is specified and data exists
	for _, w := range writes {
		if w.path == "" || w.data == nil {
			continue
		}

		if err := os.WriteFile(w.path, w.data, 0o600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", w.path, err)
		}
	}

	// Remove the pending issuer pair once it has been promoted (or was never
	// present).
	if bndl.X509.NextIssChainPEM == nil {
		for _, path := range []string{s.config.NextIssuerCertPath, s.config.NextIssuerKeyPath} {
			if path == "" {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove %s: %w", path, err)
			}
		}
	}

	return nil
}

// get retrieves the existing certificate bundle from the filesystem.
func (s *selfhosted) get(_ context.Context) (bundle.Bundle, error) {
	x509, err := s.loadAndValidateX509Bundle()
	if err != nil {
		return bundle.Bundle{}, err
	}

	jwt, err := s.loadAndValidateJWTBundle()
	if err != nil {
		return bundle.Bundle{}, err
	}

	return bundle.Bundle{
		X509: x509,
		JWT:  jwt,
	}, nil
}

// loadAndValidateX509Bundle loads the X.509 certificates and keys from disk, verifies them, and updates the bundle. Returns whether any are missing.
func (s *selfhosted) loadAndValidateX509Bundle() (*bundle.X509, error) {
	// Read trust anchors (root certificate)
	trustAnchors, err := os.ReadFile(s.config.RootCertPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read root certificate: %w", err)
	}

	// Read issuer certificate chain
	issChainPEM, err := os.ReadFile(s.config.IssuerCertPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read issuer certificate: %w", err)
	}

	// Read issuer private key
	issKeyPEM, err := os.ReadFile(s.config.IssuerKeyPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read issuer key: %w", err)
	}

	verifiedBundle, err := verifyX509Bundle(trustAnchors, issChainPEM, issKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to verify CA bundle: %w", err)
	}

	// Read the pending issuer pair written during CA renewal, if any. Either
	// both files exist, or neither: a single file indicates a torn renewal
	// write which needs manual remediation.
	nextChainPEM, chainErr := os.ReadFile(s.config.NextIssuerCertPath)
	if chainErr != nil && !os.IsNotExist(chainErr) {
		return nil, fmt.Errorf("failed to read pending issuer certificate: %w", chainErr)
	}
	nextKeyPEM, keyErr := os.ReadFile(s.config.NextIssuerKeyPath)
	if keyErr != nil && !os.IsNotExist(keyErr) {
		return nil, fmt.Errorf("failed to read pending issuer key: %w", keyErr)
	}
	if os.IsNotExist(chainErr) != os.IsNotExist(keyErr) {
		return nil, fmt.Errorf("only one of the pending issuer credentials %q and %q exists; remove the orphan file or restore the missing one", s.config.NextIssuerCertPath, s.config.NextIssuerKeyPath)
	}
	if chainErr == nil && keyErr == nil {
		if err := attachNextIssuer(verifiedBundle, nextChainPEM, nextKeyPEM); err != nil {
			return nil, err
		}
	}

	return verifiedBundle, nil
}

// loadAndValidateJWTBundle loads the JWT signing key and JWKS from disk,
// verifies them, and updates the bundle. Returns whether any JWT credentials
// are missing.
func (s *selfhosted) loadAndValidateJWTBundle() (*bundle.JWT, error) {
	// Read JWT signing key
	jwtKeyPEM, err := os.ReadFile(s.config.JWT.SigningKeyPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error reading JWT signing key: %w", err)
	}

	jwtKey, jwtErr := loadJWTSigningKey(jwtKeyPEM)
	if jwtErr != nil {
		return nil, fmt.Errorf("failed to load JWT signing key: %w", jwtErr)
	}

	// Read JWKS
	jwks, err := os.ReadFile(s.config.JWT.JWKSPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error reading JWKS: %w", err)
	}

	if verifyErr := verifyJWKS(jwks, jwtKey, s.config.JWT.KeyID); verifyErr != nil {
		return nil, fmt.Errorf("failed to verify JWKS: %w", verifyErr)
	}

	jwksK, err := jwk.Parse(jwks)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JWKS: %w", err)
	}

	return &bundle.JWT{
		SigningKey:    jwtKey,
		SigningKeyPEM: jwtKeyPEM,
		JWKS:          jwksK,
		JWKSJson:      jwks,
	}, nil
}
