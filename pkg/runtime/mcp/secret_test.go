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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

// fakeJWTFetcher is a test double for JWTFetcher.
type fakeJWTFetcher struct {
	token        string
	err          error
	lastAudience string
}

func (f *fakeJWTFetcher) FetchJWT(_ context.Context, audience string) (string, error) {
	f.lastAudience = audience
	return f.token, f.err
}

// fakeSecretGetter is a test double for SecretGetter.
type fakeSecretGetter struct {
	secrets map[string]string // key: "storeName/secretName/secretKey"
	err     error
}

func (f *fakeSecretGetter) GetSecret(_ context.Context, storeName, secretName, secretKey string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	key := fmt.Sprintf("%s/%s/%s", storeName, secretName, secretKey)
	v, ok := f.secrets[key]
	if !ok {
		return "", fmt.Errorf("secret %q not found", key)
	}
	return v, nil
}

func TestIsSecretFetchError(t *testing.T) {
	t.Run("wrapped errSecretFetch is detected", func(t *testing.T) {
		err := fmt.Errorf("OAuth2 credential retrieval failed: %w", errors.Join(errSecretFetch, errors.New("store unavailable")))
		assert.True(t, isSecretFetchError(err))
	})

	t.Run("unrelated error is not detected", func(t *testing.T) {
		assert.False(t, isSecretFetchError(errors.New("something else")))
	})

	t.Run("nil error is not detected", func(t *testing.T) {
		assert.False(t, isSecretFetchError(nil))
	})
}

func TestMakeCallToolActivity_OAuth2SecretFetchRetriesActivity(t *testing.T) {
	// When the secret store is temporarily unavailable, the activity should
	// return an error (not CallToolResult{IsError:true}) so the workflow
	// engine retries automatically.
	store := compstore.New()
	storeName := "my-store"
	store.AddMCPServer(namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			Transport: mcpserverapi.MCPTransportStreamableHTTP,
			Target:    mcpserverapi.MCPEndpointTarget{URL: "http://localhost:1"},
		},
		Auth: &mcpserverapi.MCPAuth{
			SecretStore: &storeName,
			OAuth2: &mcpserverapi.MCPOAuth2{
				Issuer: "http://example.com/token",
				SecretKeyRef: &commonapi.SecretKeyRef{
					Name: "my-secret",
					Key:  "client_secret",
				},
			},
		},
	}))

	secrets := &fakeSecretGetter{err: errors.New("secret store temporarily unavailable")}
	activity := makeCallToolActivity(ExecutorOptions{Store: store, Secrets: secrets})
	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: CallToolInput{MCPServerName: "myserver", ToolName: "greet"},
	}

	result, err := activity(actCtx)
	// The activity returns an error (retryable), not a CallToolResult.
	require.Error(t, err, "expected activity-level error for transient secret failure")
	assert.Nil(t, result, "result should be nil on retryable error")
	// The error message should be generic — no secret store or key names.
	assert.NotContains(t, err.Error(), "my-store")
	assert.NotContains(t, err.Error(), "my-secret")
	assert.NotContains(t, err.Error(), "client_secret")
	assert.Contains(t, err.Error(), "temporary authentication failure")
}
