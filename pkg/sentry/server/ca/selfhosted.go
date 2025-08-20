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
	"path/filepath"
	"runtime"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"

	"github.com/dapr/dapr/pkg/sentry/config"
	ca_bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/kit/concurrency/dir"
)

// selfhosted is a store that uses the file system as the secret store.
type selfhosted struct {
	config config.Config
}

// store saves the certificate bundle to the local filesystem.
func (s *selfhosted) store(_ context.Context, bundle ca_bundle.Bundle) error {
	dirs := make(map[string]map[string][]byte)

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

	// Write each file if the path is specified and data exists.
	for _, file := range files {
		if file.path == "" || file.data == nil {
			continue
		}

		if _, ok := dirs[filepath.Dir(file.path)]; !ok {
			dirs[filepath.Dir(file.path)] = make(map[string][]byte)
		}
		dirs[filepath.Dir(file.path)][filepath.Base(file.path)] = file.data
	}

	for dirName, files := range dirs {
		// if the directory exists and is not a symlink then we need to migrate
		// it to a symlink to allow for the atomic writes.
		if fi, lstatErr := os.Lstat(dirName); lstatErr == nil {
			if fi.Mode()&os.ModeSymlink == 0 {
				if fi.IsDir() {
					if migErr := migrateDirToSymlink(dirName); migErr != nil {
						return migErr
					}
				} else {
					return fmt.Errorf("target path %s exists and is not a directory", dirName)
				}
			}
		} else if !os.IsNotExist(lstatErr) {
			return fmt.Errorf("failed to stat %s: %w", dirName, lstatErr)
		}

		d := dir.New(dir.Options{
			Target: dirName,
			Log:    log,
		})
		if writeErr := d.Write(files); writeErr != nil {
			return fmt.Errorf("failed to write files %s: %w", dirName, writeErr)
		}

		// After writing, ensure file permissions match expectations (0600 on unix, 0666 on windows).
		perm := os.FileMode(0o600)
		if runtime.GOOS == "windows" {
			perm = 0o666
		}
		for name := range files {
			path := filepath.Join(dirName, name)
			if chmodErr := os.Chmod(path, perm); chmodErr != nil {
				return fmt.Errorf("failed to set permissions on %s: %w", path, chmodErr)
			}
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

func migrateDirToSymlink(target string) error {
	base := filepath.Dir(target)
	name := filepath.Base(target)
	versioned := filepath.Join(base, fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), name))

	// Ensure base exists
	if err := os.MkdirAll(base, os.ModePerm); err != nil {
		return fmt.Errorf("failed to ensure base dir %s: %w", base, err)
	}

	// Move the existing directory to the versioned path
	if err := os.Rename(target, versioned); err != nil {
		return fmt.Errorf("failed to move %s to %s: %w", target, versioned, err)
	}

	// Create a symlink back to the versioned dir
	if err := os.Symlink(versioned, target+".new"); err != nil {
		return fmt.Errorf("failed to create symlink %s.new -> %s: %w", target, versioned, err)
	}

	// Atomically rename the new link into place
	if err := os.Rename(target+".new", target); err != nil {
		return fmt.Errorf("failed to activate symlink at %s: %w", target, err)
	}

	return nil
}
