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
	"fmt"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcpauth "github.com/dapr/dapr/pkg/runtime/mcp/auth"
)

// ExecutorOptions configures the built-in MCP executor.
type ExecutorOptions struct {
	// Store is required; it is used to look up MCPServer manifests at call time.
	Store *compstore.ComponentStore
	// Secrets enables OAuth2 client credentials auth. If nil, OAuth2 is skipped.
	Secrets mcpauth.SecretGetter
	// JWT enables SPIFFE workload identity JWT injection. If nil, SPIFFE is skipped.
	JWT mcpauth.JWTFetcher
}

// CompstoreSecretGetter implements auth.SecretGetter using the component store's
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
