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
func (s *selfhosted) store(_ context.Context, bundle bundle.Bundle) error {
	files := make(map[string][]byte)
	files[s.config.RootCertPath] = bundle.X509.TrustAnchors
	files[s.config.IssuerCertPath] = bundle.X509.IssChainPEM
	files[s.config.IssuerKeyPath] = bundle.X509.IssKeyPEM

	if s.config.JWT.Enabled {
		files[s.config.JWT.SigningKeyPath] = bundle.JWT.SigningKeyPEM
		files[s.config.JWT.JWKSPath] = bundle.JWT.JWKSJson
	}

	// Files to write with their corresponding data
	// Write each file if the path is specified and data exists
	for path, data := range files {
		if path == "" || data == nil {
			continue
		}

		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", path, err)
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
