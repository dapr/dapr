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
	"time"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// httpTransportConfig extracts headers and auth from whichever HTTP transport
// is configured on the MCPServer. Returns nil slices/pointers for stdio.
func httpTransportConfig(server *mcpserverapi.MCPServer) ([]commonapi.NameValuePair, *mcpserverapi.MCPAuth) {
	switch {
	case server.Spec.Endpoint.StreamableHTTP != nil:
		return server.Spec.Endpoint.StreamableHTTP.Headers, server.Spec.Endpoint.StreamableHTTP.Auth
	case server.Spec.Endpoint.SSE != nil:
		return server.Spec.Endpoint.SSE.Headers, server.Spec.Endpoint.SSE.Auth
	default:
		return nil, nil
	}
}

// buildHTTPClient returns an http.Client configured with:
//  1. Static header injection from transport headers.
//  2. OAuth2 client credentials token injection (if auth.oauth2 is set and secrets != nil).
//  3. SPIFFE JWT injection via the configured header (if auth.spiffe.jwt is set and jwt != nil).
//
// OAuth2 and SPIFFE are mutually exclusive in practice; if both are configured,
// OAuth2 takes precedence and SPIFFE is ignored.
func buildHTTPClient(
	ctx context.Context,
	server *mcpserverapi.MCPServer,
	secrets SecretGetter,
	jwt JWTFetcher,
	timeout time.Duration,
) (*http.Client, error) {
	transportHeaders, auth := httpTransportConfig(server)

	// Build the resolved-header map (processor has already resolved secretKeyRef/envRef).
	headers := make(map[string]string, len(transportHeaders))
	for _, h := range transportHeaders {
		if h.Name != "" {
			headers[h.Name] = h.Value.String()
		}
	}

	// Clone the default transport so each MCP connection gets its own dial
	// settings and doesn't share state with other HTTP clients. The Timeout
	// on the http.Client is intentionally not set here — the per-call deadline
	// is managed via callCtx. However, the DialContext timeout ensures that
	// stuck TCP connections fail fast.
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Base transport: static header injection.
	var base http.RoundTripper = &headerRoundTripper{
		headers: headers,
		base:    transport,
	}

	// OAuth2 client credentials — wraps base transport with token injection.
	if auth != nil && auth.OAuth2 != nil && secrets != nil {
		client, err := buildOAuth2Client(ctx, auth, secrets, base)
		if err != nil {
			return nil, err
		}
		client.Timeout = timeout
		return client, nil
	}

	// SPIFFE JWT — wraps base transport, injects the SVID per-request.
	if auth != nil &&
		auth.SPIFFE != nil &&
		auth.SPIFFE.JWT != nil &&
		jwt != nil {
		jwtSpec := auth.SPIFFE.JWT
		return &http.Client{
			Timeout: timeout,
			Transport: &jwtRoundTripper{
				header:   jwtSpec.Header,
				prefix:   stringDeref(jwtSpec.HeaderValuePrefix),
				audience: jwtSpec.Audience,
				fetcher:  jwt,
				base:     base,
			},
		}, nil
	}

	return &http.Client{Transport: base, Timeout: timeout}, nil
}

// buildOAuth2Client fetches the client_secret from the secret store and builds
// an http.Client that injects an OAuth2 Bearer token on every request.
// The token is fetched on first use and cached until expiry by the oauth2 package.
func buildOAuth2Client(
	ctx context.Context,
	auth *mcpserverapi.MCPAuth,
	secrets SecretGetter,
	base http.RoundTripper,
) (*http.Client, error) {
	o := auth.OAuth2
	if o.SecretKeyRef == nil {
		return nil, fmt.Errorf("auth.oauth2.secretKeyRef is required for OAuth2 client credentials")
	}

	storeName := k8sStoreName
	if auth.SecretStore != nil {
		storeName = *auth.SecretStore
	}

	clientSecret, err := secrets.GetSecret(ctx, storeName, o.SecretKeyRef.Name, o.SecretKeyRef.Key)
	if err != nil {
		// Wrap with errSecretFetch so callers can detect transient secret-store
		// failures and retry the activity instead of returning a permanent error.
		return nil, fmt.Errorf("OAuth2 credential retrieval failed: %w", errors.Join(errSecretFetch, err))
	}

	cfg := clientcredentials.Config{
		// ClientID may be empty for non-standard flows (e.g. JWT-bearer assertions or
		// endpoints that key solely on client_secret); RFC 6749 client_credentials
		// requires it, so populate it from the spec when set.
		ClientID:     o.ClientID,
		ClientSecret: clientSecret,
		TokenURL:     o.Issuer,
		Scopes:       o.Scopes,
	}
	if o.Audience != nil {
		cfg.EndpointParams = map[string][]string{audienceKey: {*o.Audience}}
	}

	// oauth2.Transport wraps the base transport and injects tokens automatically.
	tokenSource := cfg.TokenSource(ctx)
	return &http.Client{
		Transport: &oauth2.Transport{
			Source: tokenSource,
			Base:   base,
		},
	}, nil
}

// headerRoundTripper is an http.RoundTripper that injects a fixed set of headers
// into every request before delegating to the underlying transport.
type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (rt *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(rt.headers) == 0 {
		return rt.base.RoundTrip(req)
	}
	// Clone the request so we do not mutate the original.
	r := req.Clone(req.Context())
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	for k, v := range rt.headers {
		r.Header.Set(k, v)
	}
	return rt.base.RoundTrip(r)
}

// jwtRoundTripper is an http.RoundTripper that fetches a SPIFFE JWT SVID for
// each request and injects it into the configured header with an optional prefix.
type jwtRoundTripper struct {
	header   string
	prefix   string
	audience string
	fetcher  JWTFetcher
	base     http.RoundTripper
}

func (rt *jwtRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// TODO: this fetches a new JWT per request. In future,
	// cache with a short TTL to avoid overloading the SPIFFE workload API under load.
	token, err := rt.fetcher.FetchJWT(req.Context(), rt.audience)
	if err != nil {
		return nil, fmt.Errorf("SPIFFE JWT fetch failed: %w", err)
	}
	r := req.Clone(req.Context())
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Set(rt.header, rt.prefix+token)
	return rt.base.RoundTrip(r)
}
