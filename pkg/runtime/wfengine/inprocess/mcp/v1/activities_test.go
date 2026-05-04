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
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

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

	httpClient, err := mcpauth.BuildHTTPClient(context.Background(), context.Background(), &server, store, nil)
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

	httpClient, err := mcpauth.BuildHTTPClient(context.Background(), context.Background(), &server, store, fetcher)
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

func realisticToolDef(t *testing.T, name string) *wfv1.MCPToolDefinition {
	t.Helper()
	desc := "tool " + name
	schemaMap := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{
				"type":        "string",
				"description": "City to query",
				"minLength":   float64(1),
				"maxLength":   float64(120),
			},
			"limit": map[string]any{
				"type":    "integer",
				"minimum": float64(1),
				"maximum": float64(100),
				"default": float64(10),
			},
			"include": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"options": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"unit":   map[string]any{"type": "string", "enum": []any{"metric", "imperial"}},
					"detail": map[string]any{"type": "boolean", "default": false},
				},
			},
		},
		"required":             []any{"city"},
		"additionalProperties": false,
	}
	schema, err := structpb.NewStruct(schemaMap)
	require.NoError(t, err, "structpb.NewStruct must accept a realistic schema")
	return &wfv1.MCPToolDefinition{
		Name:        name,
		Description: &desc,
		InputSchema: schema,
	}
}

// TestMakeListToolsActivity_CacheHitSingleCall is a sanity check that the
// cache-hit branch returns the cached tools verbatim and the response
// round-trips through protojson without error.
func TestMakeListToolsActivity_CacheHitSingleCall(t *testing.T) {
	listCache := &toolListCache{}
	listCache.store([]*wfv1.MCPToolDefinition{
		realisticToolDef(t, "alpha"),
		realisticToolDef(t, "beta"),
	})

	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: "http://unused.invalid"},
		},
	})

	// Holder is intentionally not connected — cache-hit branch must return
	// before touching it.
	activity := makeListToolsActivity(server, &SessionHolder{}, &toolSchemaCache{}, listCache)

	res, err := activity(&fakeActivityContext{ctx: context.Background()})
	require.NoError(t, err)
	listResp, ok := res.(*wfv1.ListMCPToolsResponse)
	require.True(t, ok, "expected *ListMCPToolsResponse, got %T", res)
	require.Len(t, listResp.GetTools(), 2)

	bytes, err := protojson.Marshal(listResp)
	require.NoError(t, err, "protojson.Marshal of cache-hit response must succeed")

	var rt wfv1.ListMCPToolsResponse
	require.NoError(t, protojson.Unmarshal(bytes, &rt), "protojson round-trip must succeed")
	require.Len(t, rt.GetTools(), 2)
}

// TestMakeListToolsActivity_CacheHitConcurrentRoundTrip exercises the
// cache-hit path under concurrent load with a full protojson marshal /
// unmarshal round-trip per call.
func TestMakeListToolsActivity_CacheHitConcurrentRoundTrip(t *testing.T) {
	listCache := &toolListCache{}
	const toolCount = 8
	cached := make([]*wfv1.MCPToolDefinition, toolCount)
	expectedNames := make([]string, toolCount)
	for i := range cached {
		name := "tool" + string(rune('a'+i))
		cached[i] = realisticToolDef(t, name)
		expectedNames[i] = name
	}
	sort.Strings(expectedNames)
	listCache.store(cached)

	server := namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: "http://unused.invalid"},
		},
	})

	activity := makeListToolsActivity(server, &SessionHolder{}, &toolSchemaCache{}, listCache)

	const workers = 32
	const callsPerWorker = 10

	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make(chan error, workers*callsPerWorker)

	for range workers {
		go func() {
			defer wg.Done()
			for range callsPerWorker {
				res, err := activity(&fakeActivityContext{ctx: context.Background()})
				if err != nil {
					errs <- err
					return
				}
				resp, ok := res.(*wfv1.ListMCPToolsResponse)
				if !ok {
					errs <- assert.AnError
					return
				}
				bytes, err := protojson.Marshal(resp)
				if err != nil {
					errs <- err
					return
				}
				var rt wfv1.ListMCPToolsResponse
				if err := protojson.Unmarshal(bytes, &rt); err != nil {
					errs <- err
					return
				}
				if got := len(rt.GetTools()); got != toolCount {
					errs <- assert.AnError
					return
				}
				gotNames := make([]string, len(rt.GetTools()))
				for i, td := range rt.GetTools() {
					gotNames[i] = td.GetName()
				}
				sort.Strings(gotNames)
				for i := range gotNames {
					if gotNames[i] != expectedNames[i] {
						errs <- assert.AnError
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent cache-hit call failed: %v", err)
	}
}
