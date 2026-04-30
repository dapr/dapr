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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcpauth "github.com/dapr/dapr/pkg/runtime/mcp/auth"
	fakesecurity "github.com/dapr/dapr/pkg/security/fake"
)

func TestMakeListToolsActivity_RealServer(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	})

	activity := makeListToolsActivity(server, connectTestSession(t, ts.URL), &toolSchemaCache{}, &toolListCache{})
	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: nil,
	}

	result, err := activity(actCtx)
	require.NoError(t, err)

	listResult, ok := result.(*wfv1.ListMCPToolsResponse)
	require.True(t, ok)
	require.Len(t, listResult.GetTools(), 1)
	assert.Equal(t, "greet", listResult.GetTools()[0].GetName())
}

func TestMakeListToolsActivity_CachesToolSchema(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	schemas := &toolSchemaCache{}
	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	})

	activity := makeListToolsActivity(server, connectTestSession(t, ts.URL), schemas, &toolListCache{})
	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: nil,
	}

	_, err := activity(actCtx)
	require.NoError(t, err)

	// The "greet" tool's input schema should now be cached.
	schema, ok := schemas.get("greet")
	assert.True(t, ok, "expected greet tool schema to be cached after ListTools")
	assert.NotNil(t, schema)
}

func TestMakeCallToolActivity_RealServer(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	store := compstore.New()
	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	})
	store.AddMCPServer(server)

	activity := makeCallToolActivity(server, connectTestSession(t, ts.URL), &toolSchemaCache{}, Options{Store: store})
	actCtx := &fakeActivityContext{
		ctx: context.Background(),
		input: activityCallToolInput{
			ToolName:  "greet",
			Arguments: map[string]any{"name": "dapr"},
		},
	}

	result, err := activity(actCtx)
	require.NoError(t, err)

	callResult, ok := result.(*wfv1.CallMCPToolResponse)
	require.True(t, ok)
	assert.False(t, callResult.GetIsError(), "expected success result")
	require.NotEmpty(t, callResult.GetContent())
	assert.Contains(t, callResult.GetContent()[0].GetText().GetText(), "dapr")
}

func TestMakeCallToolActivity_HeaderInjection(t *testing.T) {
	var capturedHeader string
	ts := newMCPTestServer(t, &capturedHeader)

	store := compstore.New()
	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
				URL:     ts.URL,
				Headers: []commonapi.NameValuePair{plainHeader("X-Test", "injected-value")},
			},
		},
	})
	store.AddMCPServer(server)

	httpClient, err := mcpauth.BuildHTTPClient(context.Background(), &server, store, nil)
	require.NoError(t, err)

	activity := makeCallToolActivity(server, connectTestSession(t, ts.URL, httpClient), &toolSchemaCache{}, Options{Store: store})
	actCtx := &fakeActivityContext{
		ctx: context.Background(),
		input: activityCallToolInput{
			ToolName:  "greet",
			Arguments: map[string]any{"name": "dapr"},
		},
	}

	_, err = activity(actCtx)
	require.NoError(t, err)
	assert.Equal(t, "injected-value", capturedHeader)
}

func TestMakeCallToolActivity_MissingRequiredArg(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	store := compstore.New()
	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	})
	store.AddMCPServer(server)
	schemas := &toolSchemaCache{}
	setTestSchema(t, schemas, "greet", map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []any{"name"},
	})

	activity := makeCallToolActivity(server, connectTestSession(t, ts.URL), schemas, Options{Store: store})
	actCtx := &fakeActivityContext{
		ctx: context.Background(),
		input: activityCallToolInput{
			ToolName:  "greet",
			Arguments: map[string]any{}, // missing "name" which is required
		},
	}

	result, err := activity(actCtx)
	require.NoError(t, err, "validation failure should not be an activity error")
	callResult, ok := result.(*wfv1.CallMCPToolResponse)
	require.True(t, ok)
	assert.True(t, callResult.GetIsError())
	assert.Contains(t, callResult.GetContent()[0].GetText().GetText(), "missing required")
	assert.Contains(t, callResult.GetContent()[0].GetText().GetText(), "name")
}

func TestMakeCallToolActivity_SPIFFEAuth(t *testing.T) {
	var capturedHeader string
	ts := newMCPTestServer(t, &capturedHeader)

	store := compstore.New()
	prefix := "SVID "
	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
				URL: ts.URL,
				Auth: &mcpserverapi.MCPAuth{
					SPIFFE: &mcpserverapi.SPIFFE{
						JWT: &mcpserverapi.SPIFFEJWT{
							Header:            "X-Test",
							HeaderValuePrefix: &prefix,
							Audience:          "mcp://test",
						},
					},
				},
			},
		},
	})
	store.AddMCPServer(server)

	fetcher := fakesecurity.New().WithFetchJWT(func(_ context.Context, _ string) (string, error) {
		return "svid-12345", nil
	})

	httpClient, err := mcpauth.BuildHTTPClient(context.Background(), &server, store, fetcher)
	require.NoError(t, err)

	activity := makeCallToolActivity(server, connectTestSession(t, ts.URL, httpClient), &toolSchemaCache{}, Options{Store: store, Security: fetcher})
	actCtx := &fakeActivityContext{
		ctx: context.Background(),
		input: activityCallToolInput{
			ToolName:  "greet",
			Arguments: map[string]any{"name": "dapr"},
		},
	}

	result, err := activity(actCtx)
	require.NoError(t, err)
	callResult, ok := result.(*wfv1.CallMCPToolResponse)
	require.True(t, ok)
	assert.False(t, callResult.GetIsError(), "expected success result")
	assert.Equal(t, "SVID svid-12345", capturedHeader)
}
