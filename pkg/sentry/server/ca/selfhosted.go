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
	ca_bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

// selfhosted is a store that uses the file system as the secret store.
type selfhosted struct {
	config config.Config
}

// store saves the certificate bundle to the local filesystem.
func (s *selfhosted) store(_ context.Context, bundle ca_bundle.Bundle) error {
	// Files to write with their corresponding data
	files := []struct {
		path string
		data []byte
	}{
		{s.config.RootCertPath, bundle.X509.TrustAnchors},
		{s.config.IssuerCertPath, bundle.X509.IssChainPEM},
		{s.config.IssuerKeyPath, bundle.X509.IssKeyPEM},
		{s.config.JWT.SigningKeyPath, bundle.JWT.SigningKeyPEM},
		{s.config.JWT.JWKSPath, bundle.JWT.JWKSJson},
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
func (s *selfhosted) get(_ context.Context) (ca_bundle.Bundle, ca_bundle.MissingCredentials, error) {
	var bundle ca_bundle.Bundle
	missingX509, err := s.loadAndValidateX509Bundle(&bundle)
	if err != nil {
		return ca_bundle.Bundle{}, ca_bundle.MissingCredentials{}, err
	}

	missingJWT, err := s.loadAndValidateJWTBundle(&bundle)
	if err != nil {
		return ca_bundle.Bundle{}, ca_bundle.MissingCredentials{}, err
	}

	return bundle, ca_bundle.MissingCredentials{
		X509: missingX509,
		JWT:  missingJWT,
	}, nil
}

// loadAndValidateX509Bundle loads the X.509 certificates and keys from disk, verifies them, and updates the bundle. Returns whether any are missing.
func (s *selfhosted) loadAndValidateX509Bundle(bundle *ca_bundle.Bundle) (bool, error) {
	missingX509 := false

	// Read trust anchors (root certificate)
	trustAnchors, err := os.ReadFile(s.config.RootCertPath)
	if os.IsNotExist(err) {
		missingX509 = true
	} else if err != nil {
		return false, fmt.Errorf("failed to read root certificate: %w", err)
	}

	// Read issuer certificate chain
	issChainPEM, err := os.ReadFile(s.config.IssuerCertPath)
	if os.IsNotExist(err) {
		missingX509 = true
	} else if err != nil {
		return false, fmt.Errorf("failed to read issuer certificate: %w", err)
	}

	// Read issuer private key
	issKeyPEM, err := os.ReadFile(s.config.IssuerKeyPath)
	if os.IsNotExist(err) {
		missingX509 = true
	} else if err != nil {
		return false, fmt.Errorf("failed to read issuer key: %w", err)
	}

	if !missingX509 {
		verifiedBundle, err := verifyBundle(trustAnchors, issChainPEM, issKeyPEM)
		if err != nil {
			return false, fmt.Errorf("failed to verify CA bundle: %w", err)
		}
		*bundle = verifiedBundle
	}

	return missingX509, nil
}

// loadAndValidateJWTBundle loads the JWT signing key and JWKS from disk, verifies them, and updates the bundle. Returns whether any JWT credentials are missing.
func (s *selfhosted) loadAndValidateJWTBundle(bundle *ca_bundle.Bundle) (bool, error) {
	missingJWT := false

	// Read JWT signing key
	jwtKeyPEM, err := os.ReadFile(s.config.JWT.SigningKeyPath)
	if err == nil {
		jwtKey, jwtErr := loadJWTSigningKey(jwtKeyPEM)
		if jwtErr != nil {
			return false, fmt.Errorf("failed to load JWT signing key: %w", jwtErr)
		}
		bundle.JWT.SigningKey = jwtKey
		bundle.JWT.SigningKeyPEM = jwtKeyPEM
	} else if os.IsNotExist(err) {
		missingJWT = true
	} else {
		return false, fmt.Errorf("error reading JWT signing key: %w", err)
	}

	// Read JWKS
	jwks, err := os.ReadFile(s.config.JWT.JWKSPath)
	if err == nil {
		if verifyErr := verifyJWKS(jwks, bundle.JWT.SigningKey, s.config.JWT.KeyID); verifyErr != nil {
			return false, fmt.Errorf("failed to verify JWKS: %w", verifyErr)
		}
		bundle.JWT.JWKSJson = jwks
		bundle.JWT.JWKS, err = jwk.Parse(jwks)
		if err != nil {
			return false, fmt.Errorf("failed to parse JWKS: %w", err)
		}
	} else if os.IsNotExist(err) {
		missingJWT = true
	} else {
		return false, fmt.Errorf("error reading JWKS: %w", err)
	}

	return missingJWT, nil
}
