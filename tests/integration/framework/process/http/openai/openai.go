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

// Package openai is a fake OpenAI API that authenticates callers with
// workload identity federation.
//
// The OpenAI component exchanges its JWT at https://auth.openai.com, which no
// component metadata overrides. The fake therefore serves that host over TLS
// behind an HTTP CONNECT proxy: run the client with HTTPS_PROXY set to
// ProxyURL and SSL_CERT_FILE set to CAFile. Go reads SSL_CERT_FILE on Linux
// only, so tests using this package cannot run on Windows or macOS.
package openai

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

const (
	Audience = "https://api.openai.com/v1"

	tokenHost = "auth.openai.com:443" //nolint:gosec
)

// Option configures the fake OpenAI API.
type Option func(*options)

type OpenAI struct {
	api    *http.HTTP
	auth   *http.HTTP
	proxy  net.Listener
	caFile string
	wg     sync.WaitGroup

	lock         sync.Mutex
	keySet       jwk.Set
	exchanges    []Exchange
	accessTokens map[string]bool
	completions  int
}

// Exchange is a token exchange request whose JWT was verified.
type Exchange struct {
	Token              jwt.Token
	IdentityProviderID string
	ServiceAccountID   string
}

// New returns a fake OpenAI API. Callers authenticate with WithJWKS.
func New(t *testing.T, fopts ...Option) *OpenAI {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	certPEM, keyPEM := servingCert(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, certPEM, 0o600))

	o := &OpenAI{
		keySet:       opts.keySet,
		caFile:       caFile,
		accessTokens: make(map[string]bool),
		proxy:        ports.Reserve(t, 1).Listener(t),
	}
	o.api = http.New(t, http.WithHandlerFunc("/chat/completions", o.completion))
	o.auth = http.New(t,
		http.WithHandlerFunc("/oauth/token", o.token),
		http.WithTLS(t, certPEM, keyPEM),
	)

	return o
}

// servingCert returns a self-signed certificate for the token host, which is
// also the CA clients must trust.
func servingCert(t *testing.T) ([]byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	host, _, err := net.SplitHostPort(tokenHost)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func (o *OpenAI) token(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req struct {
		GrantType          string `json:"grant_type"`
		SubjectToken       string `json:"subject_token"`
		SubjectTokenType   string `json:"subject_token_type"`
		IdentityProviderID string `json:"identity_provider_id"`
		ServiceAccountID   string `json:"service_account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.GrantType != "urn:ietf:params:oauth:grant-type:token-exchange" ||
		req.SubjectTokenType != "urn:ietf:params:oauth:token-type:jwt" {
		nethttp.Error(w, "invalid request", nethttp.StatusBadRequest)
		return
	}

	tkn, err := jwt.Parse([]byte(req.SubjectToken), jwt.WithKeySet(o.keySet), jwt.WithAudience(Audience))
	if err != nil {
		nethttp.Error(w, "invalid subject token: "+err.Error(), nethttp.StatusUnauthorized)
		return
	}

	o.lock.Lock()
	defer o.lock.Unlock()
	o.exchanges = append(o.exchanges, Exchange{
		Token:              tkn,
		IdentityProviderID: req.IdentityProviderID,
		ServiceAccountID:   req.ServiceAccountID,
	})

	accessToken := "openai-access-token-" + strconv.Itoa(len(o.exchanges))
	o.accessTokens[accessToken] = true
	fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":600}`, accessToken)
}

func (o *OpenAI) completion(w nethttp.ResponseWriter, r *nethttp.Request) {
	io.Copy(io.Discard, r.Body)

	o.lock.Lock()
	ok := o.accessTokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if ok {
		o.completions++
	}
	o.lock.Unlock()
	if !ok {
		nethttp.Error(w, "unauthorized", nethttp.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"id":"c","object":"chat.completion","choices":[{"index":0,`+
		`"message":{"role":"assistant","content":"hello from openai"},"finish_reason":"stop"}]}`)
}

// serveProxy tunnels CONNECT requests for the token host to the fake token
// endpoint, and refuses every other request.
func (o *OpenAI) serveProxy() {
	for {
		conn, err := o.proxy.Accept()
		if err != nil {
			return
		}
		o.wg.Go(func() {
			defer conn.Close()

			req, err := nethttp.ReadRequest(bufio.NewReader(conn))
			if err != nil {
				return
			}
			if req.Method != nethttp.MethodConnect || req.Host != tokenHost {
				io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
				return
			}

			upstream, err := net.Dial("tcp", "localhost:"+strconv.Itoa(o.auth.Port()))
			if err != nil {
				io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
				return
			}
			defer upstream.Close()

			io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
			go func() {
				io.Copy(upstream, conn)
				upstream.Close()
			}()
			io.Copy(conn, upstream)
		})
	}
}

// Exchanges returns the verified token exchange requests, in order.
func (o *OpenAI) Exchanges() []Exchange {
	o.lock.Lock()
	defer o.lock.Unlock()
	return append([]Exchange(nil), o.exchanges...)
}

// Completions returns the number of authenticated chat completion requests.
func (o *OpenAI) Completions() int {
	o.lock.Lock()
	defer o.lock.Unlock()
	return o.completions
}

// Endpoint is the base URL to configure the OpenAI component with.
func (o *OpenAI) Endpoint() string {
	return fmt.Sprintf("http://localhost:%d", o.api.Port())
}

// ProxyURL is the value of HTTPS_PROXY for the process running the component.
func (o *OpenAI) ProxyURL() string {
	return fmt.Sprintf("http://localhost:%d", o.proxy.Addr().(*net.TCPAddr).Port)
}

// CAFile is the value of SSL_CERT_FILE for the process running the component.
func (o *OpenAI) CAFile() string {
	return o.caFile
}

func (o *OpenAI) Run(t *testing.T, ctx context.Context) {
	o.api.Run(t, ctx)
	o.auth.Run(t, ctx)
	o.wg.Go(o.serveProxy)
}

func (o *OpenAI) Cleanup(t *testing.T) {
	require.NoError(t, o.proxy.Close())
	o.api.Cleanup(t)
	o.auth.Cleanup(t)
	o.wg.Wait()
}
