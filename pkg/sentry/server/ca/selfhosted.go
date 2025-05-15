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

	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

// selfhosted is a store that uses the file system as the secret store.
type selfhosted struct {
	config config.Config
}

// store saves the certificate bundle to the local filesystem.
func (s *selfhosted) store(_ context.Context, bundle Bundle) error {
	// Files to write with their corresponding data
	files := []struct {
		path string
		data []byte
	}{
		{s.config.RootCertPath, bundle.TrustAnchors},
		{s.config.IssuerCertPath, bundle.IssChainPEM},
		{s.config.IssuerKeyPath, bundle.IssKeyPEM},
		{s.config.JWTSigningKeyPath, bundle.JWTSigningKeyPEM},
		{s.config.JWKSPath, bundle.JWKSJson},
	}

	// Write each file if the path is specified and data exists
	for _, file := range files {
		if file.path == "" || file.data == nil {
			continue
		}

		if err := os.WriteFile(file.path, file.data, 0o600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", file.path, err)
		}
	}

	return nil
}

// get retrieves the existing certificate bundle from the filesystem.
func (s *selfhosted) get(_ context.Context) (Bundle, CredentialGenOptions, error) {
	requireX509 := false

	// Read trust anchors (root certificate)
	trustAnchors, err := os.ReadFile(s.config.RootCertPath)
	if os.IsNotExist(err) {
		requireX509 = true
	} else if err != nil {
		return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to read root certificate: %w", err)
	}

	// Read issuer certificate chain
	issChainPEM, err := os.ReadFile(s.config.IssuerCertPath)
	if os.IsNotExist(err) {
		requireX509 = true
	} else if err != nil {
		return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to read issuer certificate: %w", err)
	}

	// Read issuer private key
	issKeyPEM, err := os.ReadFile(s.config.IssuerKeyPath)
	if os.IsNotExist(err) {
		requireX509 = true
	} else if err != nil {
		return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to read issuer key: %w", err)
	}

	// If all X.509 certificates and keys exist, verify them
	var bundle Bundle
	if !requireX509 {
		bundle, err = verifyBundle(trustAnchors, issChainPEM, issKeyPEM)
		if err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to verify CA bundle: %w", err)
		}
	}

	// Check if JWT components need to be generated
	requireJWT := false

	// Read JWT signing key
	jwtKeyPEM, err := os.ReadFile(s.config.JWTSigningKeyPath)
	if err == nil {
		// JWT key exists, load it
		jwtKey, err := loadJWTSigningKey(jwtKeyPEM)
		if err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to load JWT signing key: %w", err)
		}
		bundle.JWTSigningKey = jwtKey
		bundle.JWTSigningKeyPEM = jwtKeyPEM
	} else if os.IsNotExist(err) {
		// JWT key doesn't exist, need to generate it
		requireJWT = true
	} else {
		// Unexpected error
		return Bundle{}, CredentialGenOptions{}, fmt.Errorf("error reading JWT signing key: %w", err)
	}

	// Read JWKS
	jwks, err := os.ReadFile(s.config.JWKSPath)
	if err == nil {
		// JWKS exists, verify it
		if err := verifyJWKS(jwks, bundle.JWTSigningKey); err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to verify JWKS: %w", err)
		}
		bundle.JWKSJson = jwks
		bundle.JWKS, err = jwk.Parse(jwks)
		if err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to parse JWKS: %w", err)
		}
	} else if os.IsNotExist(err) {
		// JWKS doesn't exist, need to generate it
		requireJWT = true
	} else {
		// Unexpected error
		return Bundle{}, CredentialGenOptions{}, fmt.Errorf("error reading JWKS: %w", err)
	}

	return bundle, CredentialGenOptions{
		RequireX509: requireX509,
		RequireJWT:  requireJWT,
	}, nil
}
