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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

// setTestSchema marshals a map to json.RawMessage and stores it as a tool schema.
func setTestSchema(t *testing.T, store *compstore.ComponentStore, server, tool string, schema map[string]any) {
	t.Helper()
	raw, err := json.Marshal(schema)
	require.NoError(t, err)
	store.SetMCPToolSchema(server, tool, raw)
}

// fakeActivityContext lets us test activities without the full task runtime.
type fakeActivityContext struct {
	ctx   context.Context
	input any
}

func (f *fakeActivityContext) GetInput(resultPtr any) error {
	b, err := json.Marshal(f.input)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, resultPtr)
}

func (f *fakeActivityContext) GetTaskID() int32                      { return 1 }
func (f *fakeActivityContext) GetTaskExecutionID() string            { return "test-exec-id" }
func (f *fakeActivityContext) Context() context.Context              { return f.ctx }
func (f *fakeActivityContext) GetTraceContext() *protos.TraceContext { return nil }

// newMCPTestServer creates an httptest server backed by an MCP streamable
// HTTP handler with a single "greet" tool registered.
// If capturedHeader is non-nil, it is set to the value of "X-Test" on each incoming request.
func newMCPTestServer(t *testing.T, capturedHeader *string) *httptest.Server {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1"}, nil)

	type greetIn struct {
		Name string `json:"name"`
	}
	type greetOut struct {
		Message string `json:"message"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "greet",
		Description: "Returns a greeting",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in greetIn) (*mcp.CallToolResult, greetOut, error) {
		if in.Name == "" {
			in.Name = "world"
		}
		return nil, greetOut{Message: "hello " + in.Name}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if capturedHeader != nil {
			*capturedHeader = r.Header.Get("X-Test")
		}
		return server
	}, nil)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// namedServer constructs an MCPServer with the given name and spec.
func namedServer(name string, spec mcpserverapi.MCPServerSpec) mcpserverapi.MCPServer {
	s := mcpserverapi.MCPServer{Spec: spec}
	s.Name = name
	return s
}

// plainHeader returns a NameValuePair with a plain string value.
func plainHeader(name, value string) commonapi.NameValuePair {
	return commonapi.NameValuePair{
		Name: name,
		Value: commonapi.DynamicValue{
			JSON: apiextensionsv1.JSON{Raw: []byte(`"` + value + `"`)},
		},
	}
}

func TestNewBuiltinRegistry(t *testing.T) {
	store := compstore.New()
	registry := NewBuiltinRegistry(ExecutorOptions{Store: store})
	require.NotNil(t, registry, "NewBuiltinRegistry should return a non-nil registry")
}

func TestMCPServerName(t *testing.T) {
	tests := []struct {
		orchestrationName string
		suffix            string
		want              string
	}{
		{"dapr.internal.mcp.myserver.ListTools", suffixListTools, "myserver"},
		{"dapr.internal.mcp.my-server.CallTool", suffixCallTool, "my-server"},
		{"dapr.internal.mcp.dotted.name.ListTools", suffixListTools, "dotted.name"},
		{"dapr.internal.mcp..ListTools", suffixListTools, ""},
	}
	for _, tc := range tests {
		t.Run(tc.orchestrationName, func(t *testing.T) {
			got := mcpServerName(tc.orchestrationName, tc.suffix)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCallTimeout(t *testing.T) {
	t.Run("no timeout set returns default", func(t *testing.T) {
		s := &mcpserverapi.MCPServer{}
		assert.Equal(t, defaultMCPTimeout, callTimeout(s))
	})

	t.Run("endpoint timeout is honoured", func(t *testing.T) {
		d := metav1.Duration{Duration: 5 * time.Second}
		s := &mcpserverapi.MCPServer{
			Spec: mcpserverapi.MCPServerSpec{
				Endpoint: mcpserverapi.MCPEndpoint{
					StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
						URL:     "http://example.com",
						Timeout: &d,
					},
				},
			},
		}
		assert.Equal(t, 5*time.Second, callTimeout(s))
	})
}

func TestBuildTransport_UnsupportedTransport(t *testing.T) {
	s := &mcpserverapi.MCPServer{}
	s.Name = "bad"
	_, err := buildTransport(s, http.DefaultClient)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no transport configured")
}

func TestBuildTransport_NoTransport(t *testing.T) {
	s := &mcpserverapi.MCPServer{}
	s.Name = "empty"
	_, err := buildTransport(s, http.DefaultClient)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no transport configured")
}

func TestBuildTransport_StreamableHTTP(t *testing.T) {
	s := &mcpserverapi.MCPServer{
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: "http://example.com/mcp"},
			},
		},
	}
	transport, err := buildTransport(s, http.DefaultClient)
	require.NoError(t, err)
	st, ok := transport.(*mcp.StreamableClientTransport)
	require.True(t, ok, "expected *mcp.StreamableClientTransport")
	assert.Equal(t, "http://example.com/mcp", st.Endpoint)
}

func TestBuildTransport_SSE(t *testing.T) {
	s := &mcpserverapi.MCPServer{
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				SSE: &mcpserverapi.MCPSSE{URL: "http://example.com/sse"},
			},
		},
	}
	transport, err := buildTransport(s, http.DefaultClient)
	require.NoError(t, err)
	st, ok := transport.(*mcp.SSEClientTransport)
	require.True(t, ok, "expected *mcp.SSEClientTransport")
	assert.Equal(t, "http://example.com/sse", st.Endpoint)
}

func TestConvertCallToolResult(t *testing.T) {
	t.Run("text content", func(t *testing.T) {
		r := &mcp.CallToolResult{
			IsError: false,
			Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
		}
		got := convertCallToolResult(r)
		assert.False(t, got.IsError)
		require.Len(t, got.Content, 1)
		assert.Equal(t, textContentType, got.Content[0].Type)
		assert.Equal(t, "hello", got.Content[0].Text)
	})

	t.Run("image content", func(t *testing.T) {
		r := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{Data: []byte("imgdata"), MIMEType: "image/png"},
			},
		}
		got := convertCallToolResult(r)
		require.Len(t, got.Content, 1)
		assert.Equal(t, imageContentType, got.Content[0].Type)
		assert.Equal(t, "image/png", got.Content[0].MimeType)
		assert.Equal(t, "aW1nZGF0YQ==", got.Content[0].Data) // base64("imgdata")
	})

	t.Run("audio content", func(t *testing.T) {
		r := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.AudioContent{Data: []byte("audiodata"), MIMEType: "audio/wav"},
			},
		}
		got := convertCallToolResult(r)
		require.Len(t, got.Content, 1)
		assert.Equal(t, audioContentType, got.Content[0].Type)
		assert.Equal(t, "audio/wav", got.Content[0].MimeType)
		assert.Equal(t, "YXVkaW9kYXRh", got.Content[0].Data) // base64("audiodata")
	})

	t.Run("resource link content", func(t *testing.T) {
		r := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ResourceLink{
					URI:      "file:///tmp/report.pdf",
					Name:     "report",
					MIMEType: "application/pdf",
				},
			},
		}
		got := convertCallToolResult(r)
		require.Len(t, got.Content, 1)
		assert.Equal(t, resourceLinkContentType, got.Content[0].Type)
		assert.NotNil(t, got.Content[0].Resource)
		// The raw JSON should contain the URI and name.
		assert.Contains(t, string(got.Content[0].Resource), "file:///tmp/report.pdf")
		assert.Contains(t, string(got.Content[0].Resource), "report")
	})

	t.Run("embedded resource content", func(t *testing.T) {
		r := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.EmbeddedResource{
					Resource: &mcp.ResourceContents{
						URI:  "file:///tmp/data.txt",
						Text: "some file contents",
					},
				},
			},
		}
		got := convertCallToolResult(r)
		require.Len(t, got.Content, 1)
		assert.Equal(t, resourceContentType, got.Content[0].Type)
		assert.NotNil(t, got.Content[0].Resource)
		assert.Contains(t, string(got.Content[0].Resource), "file:///tmp/data.txt")
		assert.Contains(t, string(got.Content[0].Resource), "some file contents")
	})

	t.Run("mixed content types", func(t *testing.T) {
		r := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "here is the report"},
				&mcp.ImageContent{Data: []byte("png"), MIMEType: "image/png"},
				&mcp.AudioContent{Data: []byte("mp3"), MIMEType: "audio/mp3"},
				&mcp.ResourceLink{URI: "file:///report.pdf", Name: "report"},
			},
		}
		got := convertCallToolResult(r)
		require.Len(t, got.Content, 4)
		assert.Equal(t, textContentType, got.Content[0].Type)
		assert.Equal(t, imageContentType, got.Content[1].Type)
		assert.Equal(t, audioContentType, got.Content[2].Type)
		assert.Equal(t, resourceLinkContentType, got.Content[3].Type)
	})

	t.Run("error result", func(t *testing.T) {
		r := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "err"}}}
		got := convertCallToolResult(r)
		assert.True(t, got.IsError)
	})

	t.Run("empty content", func(t *testing.T) {
		r := &mcp.CallToolResult{Content: nil}
		got := convertCallToolResult(r)
		assert.Empty(t, got.Content)
	})
}

func TestValidateToolArguments(t *testing.T) {
	store := compstore.New()

	t.Run("no cached schema passes validation", func(t *testing.T) {
		msg := validateToolArguments(store, "myserver", "greet", map[string]any{"name": "dapr"})
		assert.Empty(t, msg)
	})

	t.Run("schema with no required field passes", func(t *testing.T) {
		setTestSchema(t, store,"myserver", "greet", map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
		})
		msg := validateToolArguments(store, "myserver", "greet", map[string]any{})
		assert.Empty(t, msg)
	})

	t.Run("missing required argument fails", func(t *testing.T) {
		setTestSchema(t, store,"myserver", "weather", map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []any{"city"},
		})
		msg := validateToolArguments(store, "myserver", "weather", map[string]any{})
		assert.Contains(t, msg, "city")
		assert.Contains(t, msg, "missing required")
	})

	t.Run("all required arguments present passes", func(t *testing.T) {
		msg := validateToolArguments(store, "myserver", "weather", map[string]any{"city": "Portland"})
		assert.Empty(t, msg)
	})

	t.Run("multiple missing required arguments listed", func(t *testing.T) {
		setTestSchema(t, store,"myserver", "multi", map[string]any{
			"type":     "object",
			"required": []any{"a", "b", "c"},
		})
		msg := validateToolArguments(store, "myserver", "multi", map[string]any{"b": 1})
		assert.Contains(t, msg, "a")
		assert.Contains(t, msg, "c")
		assert.NotContains(t, msg, "\"b\"")
	})
}

func TestMakeListToolsActivity_ServerNotFound(t *testing.T) {
	store := compstore.New()
	activity := makeListToolsActivity(ExecutorOptions{Store: store})

	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: ListToolsInput{MCPServerName: "nonexistent"},
	}
	_, err := activity(actCtx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMakeListToolsActivity_RealServer(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	store := compstore.New()
	store.AddMCPServer(namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	}))

	activity := makeListToolsActivity(ExecutorOptions{Store: store})
	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: ListToolsInput{MCPServerName: "myserver"},
	}

	result, err := activity(actCtx)
	require.NoError(t, err)

	listResult, ok := result.(*ListToolsResult)
	require.True(t, ok)
	require.Len(t, listResult.Tools, 1)
	assert.Equal(t, "greet", listResult.Tools[0].Name)
}

func TestMakeListToolsActivity_CachesToolSchema(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	store := compstore.New()
	store.AddMCPServer(namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	}))

	activity := makeListToolsActivity(ExecutorOptions{Store: store})
	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: ListToolsInput{MCPServerName: "myserver"},
	}

	_, err := activity(actCtx)
	require.NoError(t, err)

	// The "greet" tool's input schema should now be cached.
	schema, ok := store.GetMCPToolSchema("myserver", "greet")
	assert.True(t, ok, "expected greet tool schema to be cached after ListTools")
	assert.NotNil(t, schema)
}

func TestMakeCallToolActivity_ServerNotFound(t *testing.T) {
	store := compstore.New()
	activity := makeCallToolActivity(ExecutorOptions{Store: store})

	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: CallToolInput{MCPServerName: "nonexistent", ToolName: "greet"},
	}
	result, err := activity(actCtx)
	require.NoError(t, err, "call-tool activity must not return activity-level error")
	callResult, ok := result.(*CallToolResult)
	require.True(t, ok)
	assert.True(t, callResult.IsError)
	require.NotEmpty(t, callResult.Content)
	assert.Contains(t, callResult.Content[0].Text, "not found")
}

func TestMakeCallToolActivity_RealServer(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	store := compstore.New()
	store.AddMCPServer(namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	}))

	activity := makeCallToolActivity(ExecutorOptions{Store: store})
	actCtx := &fakeActivityContext{
		ctx: context.Background(),
		input: CallToolInput{
			MCPServerName: "myserver",
			ToolName:      "greet",
			Arguments:     map[string]interface{}{"name": "dapr"},
		},
	}

	result, err := activity(actCtx)
	require.NoError(t, err)

	callResult, ok := result.(*CallToolResult)
	require.True(t, ok)
	assert.False(t, callResult.IsError, "unexpected error in result")
	require.NotEmpty(t, callResult.Content)
	assert.Contains(t, callResult.Content[0].Text, "dapr")
}

func TestMakeCallToolActivity_HeaderInjection(t *testing.T) {
	var capturedHeader string
	ts := newMCPTestServer(t, &capturedHeader)

	store := compstore.New()
	store.AddMCPServer(namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
				URL:     ts.URL,
				Headers: []commonapi.NameValuePair{plainHeader("X-Test", "injected-value")},
			},
		},
	}))

	activity := makeCallToolActivity(ExecutorOptions{Store: store})
	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: CallToolInput{MCPServerName: "myserver", ToolName: "greet"},
	}

	_, err := activity(actCtx)
	require.NoError(t, err)
	assert.Equal(t, "injected-value", capturedHeader)
}

func TestMakeCallToolActivity_MissingRequiredArg(t *testing.T) {
	ts := newMCPTestServer(t, nil)

	store := compstore.New()
	store.AddMCPServer(namedServer("myserver", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{URL: ts.URL},
		},
	}))
	setTestSchema(t, store,"myserver", "greet", map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []any{"name"},
	})

	activity := makeCallToolActivity(ExecutorOptions{Store: store})
	actCtx := &fakeActivityContext{
		ctx:   context.Background(),
		input: CallToolInput{MCPServerName: "myserver", ToolName: "greet", Arguments: map[string]any{}},
	}

	result, err := activity(actCtx)
	require.NoError(t, err, "validation failure should not be an activity error")
	callResult, ok := result.(*CallToolResult)
	require.True(t, ok)
	assert.True(t, callResult.IsError)
	require.NotEmpty(t, callResult.Content)
	assert.Contains(t, callResult.Content[0].Text, "missing required")
	assert.Contains(t, callResult.Content[0].Text, "name")
}

func TestMakeCallToolActivity_SPIFFEAuth(t *testing.T) {
	var capturedHeader string
	ts := newMCPTestServer(t, &capturedHeader)

	store := compstore.New()
	prefix := "SVID "
	store.AddMCPServer(namedServer("myserver", mcpserverapi.MCPServerSpec{
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
	}))

	fetcher := &fakeJWTFetcher{token: "svid-12345"}
	activity := makeCallToolActivity(ExecutorOptions{Store: store, JWT: fetcher})
	actCtx := &fakeActivityContext{
		ctx: context.Background(),
		input: CallToolInput{
			MCPServerName: "myserver",
			ToolName:      "greet",
			Arguments:     map[string]any{"name": "dapr"},
		},
	}

	result, err := activity(actCtx)
	require.NoError(t, err)
	callResult, ok := result.(*CallToolResult)
	require.True(t, ok)
	assert.False(t, callResult.IsError)
	assert.Equal(t, "SVID svid-12345", capturedHeader)
}
