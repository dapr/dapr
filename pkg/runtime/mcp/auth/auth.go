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

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	autherrors "github.com/dapr/dapr/pkg/runtime/mcp/auth/errors"
	"github.com/dapr/dapr/pkg/security"
)

const (
	k8sStoreName = "kubernetes"
	audienceKey  = "audience"
)

// HTTPTransportConfig extracts headers and auth from whichever HTTP transport
// is configured on the MCPServer. Returns nil slices/pointers for stdio.
func HTTPTransportConfig(server *mcpserverapi.MCPServer) ([]commonapi.NameValuePair, *mcpserverapi.MCPAuth) {
	switch {
	case server.Spec.Endpoint.StreamableHTTP != nil:
		return server.Spec.Endpoint.StreamableHTTP.Headers, server.Spec.Endpoint.StreamableHTTP.Auth
	case server.Spec.Endpoint.SSE != nil:
		return server.Spec.Endpoint.SSE.Headers, server.Spec.Endpoint.SSE.Auth
	default:
		return nil, nil
	}
}

// BuildHTTPClient returns an http.Client configured with:
//  1. Static header injection from transport headers.
//  2. OAuth2 client credentials token injection (if auth.oauth2 is set and secrets != nil).
//  3. SPIFFE JWT injection via the configured header (if auth.spiffe.jwt is set and jwt != nil).
//
// OAuth2 and SPIFFE are mutually exclusive in practice; if both are configured,
// OAuth2 takes precedence and SPIFFE is ignored.
func BuildHTTPClient(
	ctx context.Context,
	server *mcpserverapi.MCPServer,
	secrets *compstore.ComponentStore,
	jwt security.Handler,
) (*http.Client, error) {
	transportHeaders, authCfg := HTTPTransportConfig(server)

	// Build the resolved-header map (processor has already resolved secretKeyRef/envRef).
	headers := make(map[string]string, len(transportHeaders))
	for _, h := range transportHeaders {
		if h.Name != "" {
			headers[h.Name] = h.Value.String()
		}
	}

	// Clone the default transport so each MCP connection gets its own dial
	// settings and doesn't share state with other HTTP clients. The returned
	// http.Clients set Timeout as an overall request bound; per-call contexts
	// still control cancellation/deadlines, and the DialContext timeout on the
	// cloned transport ensures stuck TCP connections fail fast.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	var authTransport http.RoundTripper = transport

	// OAuth2 client credentials — wraps raw transport with token injection.
	if authCfg != nil && authCfg.OAuth2 != nil && secrets != nil {
		client, err := buildOAuth2Client(ctx, authCfg, secrets, authTransport)
		if err != nil {
			return nil, err
		}
		// Wrap the oauth2 client's transport with static header injection.
		client.Transport = &headerRoundTripper{headers: headers, base: client.Transport}
		return client, nil
	}

	// SPIFFE JWT — wraps raw transport, injects the SVID per-request.
	if authCfg != nil &&
		authCfg.SPIFFE != nil &&
		authCfg.SPIFFE.JWT != nil &&
		jwt != nil {
		jwtSpec := authCfg.SPIFFE.JWT
		return &http.Client{
			Transport: &headerRoundTripper{
				headers: headers,
				base: &jwtRoundTripper{
					header:   jwtSpec.Header,
					prefix:   jwtSpec.HeaderValuePrefix,
					audience: jwtSpec.Audience,
					fetcher:  jwt,
					base:     authTransport,
				},
			},
		}, nil
	}

	// Do NOT set http.Client.Timeout here. The MCP Go SDK manages
	// long-lived SSE streams on the same HTTP client, and a global
	// timeout would kill those connections. Per-call deadlines are
	// enforced at the activity level via context.WithTimeout instead.
	return &http.Client{
		Transport: &headerRoundTripper{headers: headers, base: transport},
	}, nil
}

// buildOAuth2Client fetches the client_secret from the secret store and builds
// an http.Client that injects an OAuth2 Bearer token on every request.
func buildOAuth2Client(
	ctx context.Context,
	authCfg *mcpserverapi.MCPAuth,
	secrets *compstore.ComponentStore,
	base http.RoundTripper,
) (*http.Client, error) {
	o := authCfg.OAuth2
	if o.SecretKeyRef == nil {
		return nil, errors.New("auth.oauth2.secretKeyRef is required for OAuth2 client credentials")
	}

	storeName := k8sStoreName
	if authCfg.SecretStore != nil {
		storeName = *authCfg.SecretStore
	}

	clientSecret, err := secrets.GetSecret(ctx, storeName, o.SecretKeyRef.Name, o.SecretKeyRef.Key)
	if err != nil {
		return nil, fmt.Errorf("OAuth2 credential retrieval failed: %w", errors.Join(autherrors.ErrSecretFetch, err))
	}

	cfg := clientcredentials.Config{
		ClientID:     stringDeref(o.ClientID),
		ClientSecret: clientSecret,
		TokenURL:     o.Issuer,
		Scopes:       o.Scopes,
	}
	if o.Audience != nil {
		cfg.EndpointParams = map[string][]string{audienceKey: {*o.Audience}}
	}

	tokenSource := cfg.TokenSource(ctx)
	return &http.Client{
		Transport: &oauth2.Transport{
			Source: tokenSource,
			Base:   base,
		},
	}, nil
}

type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (rt *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(rt.headers) == 0 {
		return rt.base.RoundTrip(req)
	}
	r := req.Clone(req.Context())
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	for k, v := range rt.headers {
		r.Header.Set(k, v)
	}
	return rt.base.RoundTrip(r)
}

type jwtRoundTripper struct {
	header   string
	prefix   *string
	audience string
	fetcher  security.Handler
	base     http.RoundTripper
}

func (rt *jwtRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := rt.fetcher.FetchJWT(req.Context(), rt.audience)
	if err != nil {
		return nil, fmt.Errorf("SPIFFE JWT fetch failed: %w", err)
	}
	r := req.Clone(req.Context())
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	val := token
	if rt.prefix != nil {
		val = *rt.prefix + token
	}
	r.Header.Set(rt.header, val)
	return rt.base.RoundTrip(r)
}

// stringDeref returns the dereferenced string or "" if nil.
func stringDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
