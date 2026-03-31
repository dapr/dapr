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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
)

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
	fetcher := &fakeJWTFetcher{token: "my-jwt-token"}
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
	assert.Equal(t, "https://api.example.com", fetcher.lastAudience)
}

func TestJWTRoundTripper_FetchError(t *testing.T) {
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	fetcher := &fakeJWTFetcher{err: errors.New("svid unavailable")}
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
			Auth: &mcpserverapi.MCPAuth{
				SPIFFE: &mcpserverapi.SPIFFESpec{
					JWT: &mcpserverapi.SPIFFEJWTSpec{
						Header:   "X-SVID",
						Audience: "mcp://payments",
						Prefix:   &prefix,
					},
				},
			},
		},
	}

	fetcher := &fakeJWTFetcher{token: "spiffe-svid-token"}
	client, err := buildHTTPClient(context.Background(), srv, nil, fetcher)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.NoError(t, err)

	assert.Equal(t, "Bearer spiffe-svid-token", captured.Get("X-SVID"))
	assert.Equal(t, "mcp://payments", fetcher.lastAudience)
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
			Auth: &mcpserverapi.MCPAuth{
				SPIFFE: &mcpserverapi.SPIFFESpec{
					JWT: &mcpserverapi.SPIFFEJWTSpec{
						Header:   "X-JWT",
						Audience: "aud",
					},
				},
			},
		},
	}

	fetcher := &fakeJWTFetcher{token: "raw-token"}
	client, err := buildHTTPClient(context.Background(), srv, nil, fetcher)
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
	}

	secrets := &fakeSecretGetter{
		secrets: map[string]string{
			"my-store/my-secret/client_secret": "super-secret",
		},
	}

	client, err := buildHTTPClient(context.Background(), srv, secrets, nil)
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
	}

	secrets := &fakeSecretGetter{err: errors.New("secret store unavailable")}
	_, err := buildHTTPClient(context.Background(), srv, secrets, nil)
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
		Headers: []commonapi.NameValuePair{plainHeader("X-Static", "hello")},
	})
	client, err := buildHTTPClient(context.Background(), &srv, nil, nil)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.NoError(t, err)

	assert.Equal(t, "hello", captured.Get("X-Static"))
}
