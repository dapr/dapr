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

package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

// SecretGetter fetches a single secret value from a named Dapr secret store.
// storeName is the name of the registered secret store component.
// secretName and secretKey identify the secret and field within it.
type SecretGetter interface {
	GetSecret(ctx context.Context, storeName, secretName, secretKey string) (string, error)
}

// JWTFetcher fetches a SPIFFE JWT SVID for the given audience.
// The returned string is the raw JWT compact serialisation (header.payload.signature).
type JWTFetcher interface {
	FetchJWT(ctx context.Context, audience string) (string, error)
}

// ExecutorOptions configures the built-in MCP executor.
type ExecutorOptions struct {
	// Store is required; it is used to look up MCPServer manifests at call time.
	Store *compstore.ComponentStore
	// Secrets enables OAuth2 client credentials auth. If nil, OAuth2 is skipped.
	Secrets SecretGetter
	// JWT enables SPIFFE workload identity JWT injection. If nil, SPIFFE is skipped.
	JWT JWTFetcher
}

// CompstoreSecretGetter implements SecretGetter using the component store's
// registered secret store components.
type CompstoreSecretGetter struct {
	store *compstore.ComponentStore
}

// NewCompstoreSecretGetter returns a SecretGetter backed by the component store.
func NewCompstoreSecretGetter(store *compstore.ComponentStore) *CompstoreSecretGetter {
	return &CompstoreSecretGetter{store: store}
}

func (g *CompstoreSecretGetter) GetSecret(ctx context.Context, storeName, secretName, secretKey string) (string, error) {
	ss, ok := g.store.GetSecretStore(storeName)
	if !ok {
		return "", fmt.Errorf("secret store %q not found", storeName)
	}
	resp, err := ss.GetSecret(ctx, secretstores.GetSecretRequest{Name: secretName})
	if err != nil {
		return "", fmt.Errorf("failed to get secret %q from store %q: %w", secretName, storeName, err)
	}
	val, ok := resp.Data[secretKey]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %q (store: %q)", secretKey, secretName, storeName)
	}
	return val, nil
}

// errSecretFetch is a sentinel error wrapped by buildOAuth2Client when the
// secret store fetch fails. This allows callers to distinguish transient
// secret-store failures (retryable) from permanent config errors.
var errSecretFetch = errors.New("secret fetch failed")

// isSecretFetchError returns true when err wraps errSecretFetch.
func isSecretFetchError(err error) bool {
	return errors.Is(err, errSecretFetch)
}
