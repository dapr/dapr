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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
)

// setTestSchema marshals a map to json.RawMessage and stores it in the schema cache.
func setTestSchema(t *testing.T, cache *toolSchemaCache, tool string, schema map[string]any) {
	t.Helper()
	raw, err := json.Marshal(schema)
	require.NoError(t, err)
	cache.set(tool, raw)
}

// connectTestSession creates a SessionHolder connected to the given URL.
// An optional *http.Client can be provided to inject custom headers or auth.
func connectTestSession(t *testing.T, url string, httpClient ...*http.Client) *SessionHolder {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: url}
	if len(httpClient) > 0 && httpClient[0] != nil {
		transport.HTTPClient = httpClient[0]
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := c.Connect(context.Background(), transport, nil)
	require.NoError(t, err)
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	h := &SessionHolder{
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
	}
	h.session.Store(session)
	t.Cleanup(func() { h.Close() })
	return h
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

func (f *fakeActivityContext) GetTaskID() int32                             { return 1 }
func (f *fakeActivityContext) GetTaskExecutionID() string                   { return "test-exec-id" }
func (f *fakeActivityContext) Context() context.Context                     { return f.ctx }
func (f *fakeActivityContext) GetTraceContext() *protos.TraceContext        { return nil }
func (f *fakeActivityContext) GetPropagatedHistory() *api.PropagatedHistory { return nil }

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
func namedServer(name string, srvSpec mcpserverapi.MCPServerSpec) mcpserverapi.MCPServer {
	s := mcpserverapi.MCPServer{Spec: srvSpec}
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

func TestCallTimeout(t *testing.T) {
	t.Run("no timeout set returns default", func(t *testing.T) {
		s := &mcpserverapi.MCPServer{}
		assert.Equal(t, defaultMCPTimeout, CallTimeout(s))
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
		assert.Equal(t, 5*time.Second, CallTimeout(s))
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

// TestSDKContentMarshalsSpecShape locks in the contract that the upstream
// MCP SDK content types serialize to the MCP wire spec — i.e. flat tagged
// unions with a `type` discriminator. This is the property that lets daprd
// return `*mcp.CallToolResult` directly from activities without a translation
// layer. If the SDK ever drops a custom MarshalJSON or changes the wire
// shape, this test fails loudly.
func TestSDKContentMarshalsSpecShape(t *testing.T) {
	cases := []struct {
		name        string
		content     mcp.Content
		wantType    string
		wantSubstrs []string
	}{
		{
			name:        "text",
			content:     &mcp.TextContent{Text: "hello"},
			wantType:    "text",
			wantSubstrs: []string{`"text":"hello"`},
		},
		{
			name:        "image",
			content:     &mcp.ImageContent{Data: []byte("imgdata"), MIMEType: "image/png"},
			wantType:    "image",
			wantSubstrs: []string{`"mimeType":"image/png"`, `"data":"aW1nZGF0YQ=="`},
		},
		{
			name:        "audio",
			content:     &mcp.AudioContent{Data: []byte("audio"), MIMEType: "audio/wav"},
			wantType:    "audio",
			wantSubstrs: []string{`"mimeType":"audio/wav"`},
		},
		{
			name: "resource_link",
			content: &mcp.ResourceLink{
				URI: "file:///tmp/report.pdf", Name: "report", MIMEType: "application/pdf",
			},
			wantType:    "resource_link",
			wantSubstrs: []string{`"uri":"file:///tmp/report.pdf"`, `"name":"report"`},
		},
		{
			name: "embedded resource",
			content: &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI: "file:///tmp/data.txt", Text: "some file contents",
			}},
			wantType:    "resource",
			wantSubstrs: []string{`"uri":"file:///tmp/data.txt"`, `"text":"some file contents"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.content)
			require.NoError(t, err)
			assert.Contains(t, string(b), `"type":"`+tc.wantType+`"`,
				"each MCP content type must include its `type` discriminator on the wire")
			for _, sub := range tc.wantSubstrs {
				assert.Contains(t, string(b), sub)
			}
		})
	}
}
