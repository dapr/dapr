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

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/dapr/components-contrib/secretstores"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	fakesecurity "github.com/dapr/dapr/pkg/security/fake"
)

// fakeSecretStore implements secretstores.SecretStore for tests.
type fakeSecretStore struct {
	data map[string]string // secretName -> value (single key)
	err  error
}

func (f *fakeSecretStore) GetSecret(_ context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	if f.err != nil {
		return secretstores.GetSecretResponse{}, f.err
	}
	// Return all configured data keyed by their original keys.
	// compstore.GetSecret looks up resp.Data[secretKey] where secretKey is
	// what the MCPServer spec references in secretKeyRef.key.
	return secretstores.GetSecretResponse{Data: f.data}, nil
}

func (f *fakeSecretStore) Init(_ context.Context, _ secretstores.Metadata) error { return nil }
func (f *fakeSecretStore) BulkGetSecret(_ context.Context, _ secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	return secretstores.BulkGetSecretResponse{}, nil
}
func (f *fakeSecretStore) Close() error                                          { return nil }
func (f *fakeSecretStore) Features() []secretstores.Feature                      { return nil }
func (f *fakeSecretStore) GetComponentMetadata() (map[string]string, error)      { return nil, nil }

// newTestCompstore creates a *compstore.ComponentStore with a fake secret store.
func newTestCompstore(t *testing.T, storeName string, secrets map[string]string, err error) *compstore.ComponentStore {
	t.Helper()
	cs := compstore.New()
	cs.AddSecretStore(storeName, &fakeSecretStore{data: secrets, err: err})
	return cs
}


func namedServer(name string, spec mcpserverapi.MCPServerSpec) mcpserverapi.MCPServer {
	s := mcpserverapi.MCPServer{Spec: spec}
	s.Name = name
	return s
}

func plainHeader(name, value string) commonapi.NameValuePair {
	return commonapi.NameValuePair{
		Name: name,
		Value: commonapi.DynamicValue{
			JSON: apiextensionsv1.JSON{Raw: []byte(`"` + value + `"`)},
		},
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeTokenServer returns a minimal OAuth2 token endpoint that accepts any
// client_secret and returns the given access token.
func fakeTokenServer(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, accessToken)
	}))
}

func TestHeaderRoundTripper(t *testing.T) {
	var captured http.Header

	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r.Header.Clone()
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})

	rt := &headerRoundTripper{
		headers: map[string]string{"X-Custom": "value1", "Authorization": "Bearer tok"},
		base:    inner,
	}

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	assert.Equal(t, "value1", captured.Get("X-Custom"))
	assert.Equal(t, "Bearer tok", captured.Get("Authorization"))
}

func TestHeaderRoundTripper_NoHeaders(t *testing.T) {
	callCount := 0
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		callCount++
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	rt := &headerRoundTripper{headers: nil, base: inner}
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, 1, callCount)
}

func TestJWTRoundTripper_InjectsHeader(t *testing.T) {
	var captured http.Header
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r.Header.Clone()
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})

	prefix := "Bearer "
	var lastAudience string
	fetcher := fakesecurity.New().WithFetchJWT(func(_ context.Context, audience string) (string, error) {
		lastAudience = audience
		return "my-jwt-token", nil
	})
	rt := &jwtRoundTripper{
		header:   "Authorization",
		prefix:   prefix,
		audience: "https://api.example.com",
		fetcher:  fetcher,
		base:     inner,
	}

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "Bearer my-jwt-token", captured.Get("Authorization"))
	assert.Equal(t, "https://api.example.com", lastAudience)
}

func TestJWTRoundTripper_FetchError(t *testing.T) {
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	fetcher := fakesecurity.New().WithFetchJWT(func(_ context.Context, _ string) (string, error) {
		return "", errors.New("svid unavailable")
	})
	rt := &jwtRoundTripper{
		header: "Authorization", prefix: "Bearer ", audience: "aud",
		fetcher: fetcher, base: inner,
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	_, err = rt.RoundTrip(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "svid unavailable")
}

func TestBuildHTTPClient_SPIFFEInjectsJWT(t *testing.T) {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	prefix := "Bearer "
	srv := &mcpserverapi.MCPServer{
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL: ts.URL,
					Auth: &mcpserverapi.MCPAuth{
						SPIFFE: &mcpserverapi.SPIFFE{
							JWT: &mcpserverapi.SPIFFEJWT{
								Header:            "X-SVID",
								Audience:          "mcp://payments",
								HeaderValuePrefix: &prefix,
							},
						},
					},
				},
			},
		},
	}

	var lastAudience string
	fetcher := fakesecurity.New().WithFetchJWT(func(_ context.Context, audience string) (string, error) {
		lastAudience = audience
		return "spiffe-svid-token", nil
	})
	client, err := BuildHTTPClient(context.Background(), srv, nil, fetcher, 30*time.Second)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.NoError(t, err)

	assert.Equal(t, "Bearer spiffe-svid-token", captured.Get("X-SVID"))
	assert.Equal(t, "mcp://payments", lastAudience)
}

func TestBuildHTTPClient_SPIFFEWithoutPrefix(t *testing.T) {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	srv := &mcpserverapi.MCPServer{
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL: ts.URL,
					Auth: &mcpserverapi.MCPAuth{
						SPIFFE: &mcpserverapi.SPIFFE{
							JWT: &mcpserverapi.SPIFFEJWT{
								Header:   "X-JWT",
								Audience: "aud",
							},
						},
					},
				},
			},
		},
	}

	fetcher := fakesecurity.New().WithFetchJWT(func(_ context.Context, _ string) (string, error) {
		return "raw-token", nil
	})
	client, err := BuildHTTPClient(context.Background(), srv, nil, fetcher, 30*time.Second)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.NoError(t, err)

	assert.Equal(t, "raw-token", captured.Get("X-JWT"))
}

func TestBuildHTTPClient_OAuth2InjectsBearer(t *testing.T) {
	tokenServer := fakeTokenServer(t, "oauth-access-token")
	defer tokenServer.Close()

	var captured http.Header
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()

	storeName := "my-store"
	srv := &mcpserverapi.MCPServer{
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL: targetServer.URL,
					Auth: &mcpserverapi.MCPAuth{
						SecretStore: &storeName,
						OAuth2: &mcpserverapi.MCPOAuth2{
							Issuer: tokenServer.URL,
							SecretKeyRef: &commonapi.SecretKeyRef{
								Name: "my-secret",
								Key:  "client_secret",
							},
						},
					},
				},
			},
		},
	}

	cs := newTestCompstore(t, "my-store", map[string]string{"client_secret": "super-secret"}, nil)

	client, err := BuildHTTPClient(context.Background(), srv, cs, nil, 30*time.Second)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, targetServer.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.NoError(t, err)

	assert.Equal(t, "Bearer oauth-access-token", captured.Get("Authorization"))
}

func TestBuildHTTPClient_OAuth2SecretFetchError(t *testing.T) {
	srv := &mcpserverapi.MCPServer{
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL: "http://example.com",
					Auth: &mcpserverapi.MCPAuth{
						OAuth2: &mcpserverapi.MCPOAuth2{
							Issuer: "http://example.com/token",
							SecretKeyRef: &commonapi.SecretKeyRef{
								Name: "missing-secret",
								Key:  "client_secret",
							},
						},
					},
				},
			},
		},
	}

	cs := newTestCompstore(t, "kubernetes", nil, errors.New("secret store unavailable"))
	_, err := BuildHTTPClient(context.Background(), srv, cs, nil, 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret store unavailable")
}

func TestBuildHTTPClient_StaticHeadersNoAuth(t *testing.T) {
	var captured http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	srv := namedServer("s", mcpserverapi.MCPServerSpec{
		Endpoint: mcpserverapi.MCPEndpoint{
			StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
				URL:     ts.URL,
				Headers: []commonapi.NameValuePair{plainHeader("X-Static", "hello")},
			},
		},
	})
	client, err := BuildHTTPClient(context.Background(), &srv, nil, nil, 30*time.Second)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.NoError(t, err)

	assert.Equal(t, "hello", captured.Get("X-Static"))
}
