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

// Package anthropic is a fake Anthropic API that authenticates callers with
// workload identity federation: JWTs are verified against a JWK set and can
// be exchanged only once, as the real API enforces.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"

	"github.com/dapr/dapr/tests/integration/framework/process/http"
)

const Audience = "https://api.anthropic.com"

type Anthropic struct {
	http *http.HTTP

	lock         sync.Mutex
	keySet       jwk.Set
	exchanged    []jwt.Token
	accessTokens map[string]bool
	usedJTIs     map[string]bool
	messages     int
}

// New returns a fake Anthropic API trusting JWTs signed by keys in keySet.
func New(t *testing.T, keySet jwk.Set) *Anthropic {
	t.Helper()

	a := &Anthropic{
		keySet:       keySet,
		accessTokens: make(map[string]bool),
		usedJTIs:     make(map[string]bool),
	}
	a.http = http.New(t,
		http.WithHandlerFunc("/v1/oauth/token", a.token),
		http.WithHandlerFunc("/v1/messages", a.message),
	)

	return a
}

func (a *Anthropic) token(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req struct {
		GrantType string `json:"grant_type"`
		Assertion string `json:"assertion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GrantType != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		nethttp.Error(w, "invalid request", nethttp.StatusBadRequest)
		return
	}

	tkn, err := jwt.Parse([]byte(req.Assertion), jwt.WithKeySet(a.keySet), jwt.WithAudience(Audience))
	if err != nil {
		nethttp.Error(w, "invalid assertion: "+err.Error(), nethttp.StatusUnauthorized)
		return
	}

	a.lock.Lock()
	defer a.lock.Unlock()
	if a.usedJTIs[tkn.JwtID()] {
		nethttp.Error(w, "jti_reused", nethttp.StatusUnauthorized)
		return
	}
	a.usedJTIs[tkn.JwtID()] = true
	a.exchanged = append(a.exchanged, tkn)

	accessToken := "sk-ant-oat01-" + strconv.Itoa(len(a.exchanged))
	a.accessTokens[accessToken] = true
	// expires_in is inside the client refresh window, so every request
	// exchanges a new JWT, exercising the single-use jti check.
	fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":60}`, accessToken)
}

func (a *Anthropic) message(w nethttp.ResponseWriter, r *nethttp.Request) {
	io.Copy(io.Discard, r.Body)

	a.lock.Lock()
	ok := a.accessTokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if ok {
		a.messages++
	}
	a.lock.Unlock()
	if !ok || r.Header.Get("x-api-key") != "" {
		nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"id":"msg","type":"message","role":"assistant","model":"claude",`+
		`"content":[{"type":"text","text":"hello from claude"}],"stop_reason":"end_turn",`+
		`"usage":{"input_tokens":1,"output_tokens":1}}`)
}

// Exchanged returns the verified JWTs exchanged for access tokens, in order.
func (a *Anthropic) Exchanged() []jwt.Token {
	a.lock.Lock()
	defer a.lock.Unlock()
	return append([]jwt.Token(nil), a.exchanged...)
}

// Messages returns the number of authenticated messages requests.
func (a *Anthropic) Messages() int {
	a.lock.Lock()
	defer a.lock.Unlock()
	return a.messages
}

// Endpoint is the base URL to configure the Anthropic component with.
func (a *Anthropic) Endpoint() string {
	return fmt.Sprintf("http://localhost:%d/v1", a.http.Port())
}

func (a *Anthropic) Run(t *testing.T, ctx context.Context) {
	a.http.Run(t, ctx)
}

func (a *Anthropic) Cleanup(t *testing.T) {
	a.http.Cleanup(t)
}
