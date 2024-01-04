/*
Copyright 2023 The Dapr Authors
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

package legacy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serialNumber(t *testing.T) *big.Int {
	t.Helper()
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)

	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	require.NoError(t, err)
	return serialNumber
}

func genIssuerCA(t *testing.T) (issuerCA *x509.Certificate, issuerKey *ecdsa.PrivateKey, rootCA x509bundle.Source, rootPool *x509.CertPool) {
	t.Helper()

	rootPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := x509.Certificate{
		SerialNumber:          serialNumber(t),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Minute),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &rootPK.PublicKey, rootPK)
	require.NoError(t, err)

	rootCert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	issPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl = x509.Certificate{
		SerialNumber:          serialNumber(t),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Minute),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	issCertDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &issPK.PublicKey, rootPK)
	require.NoError(t, err)

	issCert, err := x509.ParseCertificate(issCertDER)
	require.NoError(t, err)

	rootPool = x509.NewCertPool()
	rootPool.AddCert(rootCert)

	return issCert, issPK,
		x509bundle.FromX509Authorities(spiffeid.RequireTrustDomainFromString("example.com"), []*x509.Certificate{rootCert}),
		rootPool
}

func Test_NewServer(t *testing.T) {
	issCert, issKey, rootCA, rootPool := genIssuerCA(t)
	diffCert, diffKey, diffRootCA, diffPool := genIssuerCA(t)

	clientPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	clientPKDER, err := x509.MarshalPKCS8PrivateKey(clientPK)
	require.NoError(t, err)

	serverPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serverCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		URIs:         []*url.URL{spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.com"), "server").URL()},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Minute),
		SerialNumber: serialNumber(t),
	}, issCert, &serverPK.PublicKey, issKey)
	require.NoError(t, err)

	serverPKDER, err := x509.MarshalPKCS8PrivateKey(serverPK)
	require.NoError(t, err)

	serverSVID, err := x509svid.ParseRaw(append(serverCertDER, issCert.Raw...), serverPKDER)
	require.NoError(t, err)

	dial := func(t *testing.T, clientConfig *tls.Config) error {
		var lis net.Listener
		var lock sync.Mutex

		server := &http.Server{
			Addr:              "localhost:0",
			TLSConfig:         NewServer(serverSVID, rootCA, tlsconfig.AuthorizeAny()),
			ReadHeaderTimeout: time.Second,
		}
		server.BaseContext = func(nlis net.Listener) context.Context {
			lock.Lock()
			defer lock.Unlock()
			lis = nlis
			return context.Background()
		}

		serverClosed := make(chan struct{})
		go func() {
			defer close(serverClosed)
			require.ErrorIs(t, server.ListenAndServeTLS("", ""), http.ErrServerClosed)
		}()

		t.Cleanup(func() {
			require.NoError(t, server.Close())
			select {
			case <-serverClosed:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for server to close")
			}
		})

		assert.Eventually(t, func() bool {
			lock.Lock()
			defer lock.Unlock()
			return lis != nil
		}, time.Second, time.Millisecond)

		client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientConfig}}
		conn, err := client.Get(fmt.Sprintf("https://localhost:%d/", lis.Addr().(*net.TCPAddr).Port))
		if err != nil {
			return err
		}
		conn.Body.Close()
		return nil
	}

	t.Run("if client uses a SVID in the same root with the correct Trust Domain, no error", func(t *testing.T) {
		id := spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.com"), "client")

		clientCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			URIs:         []*url.URL{id.URL()},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &clientPK.PublicKey, issKey)
		require.NoError(t, err)

		clientsvid, err := x509svid.ParseRaw(append(clientCertDER, issCert.Raw...), clientPKDER)
		require.NoError(t, err)

		require.NoError(t, dial(t, tlsconfig.MTLSClientConfig(clientsvid, rootCA, tlsconfig.AuthorizeAny())))
	})

	t.Run("if client uses a SVID but signed by different root, expect error", func(t *testing.T) {
		id := spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.com"), "client")

		clientCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			URIs:         []*url.URL{id.URL()},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, diffCert, &clientPK.PublicKey, diffKey)
		require.NoError(t, err)

		clientsvid, err := x509svid.ParseRaw(append(clientCertDER, diffCert.Raw...), clientPKDER)
		require.NoError(t, err)

		err = dial(t, tlsconfig.MTLSClientConfig(clientsvid, diffRootCA, tlsconfig.AuthorizeAny()))
		require.ErrorContains(t, err, "x509: ECDSA verification failure")
	})

	t.Run("if client uses DNS and is `cluster.local`, expect no error", func(t *testing.T) {
		clientCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"cluster.local"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &clientPK.PublicKey, issKey)
		require.NoError(t, err)

		require.NoError(t, dial(t, &tls.Config{
			RootCAs: rootPool,

			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{clientCertDER, issCert.Raw}, PrivateKey: clientPK},
			},
		},
		))
	})

	t.Run("if client uses DNS and one is `cluster.local`, expect no error", func(t *testing.T) {
		clientCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"no-cluster.foo.local", "cluster.local"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &clientPK.PublicKey, issKey)
		require.NoError(t, err)

		require.NoError(t, dial(t, &tls.Config{
			RootCAs:            rootPool,
			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{clientCertDER, issCert.Raw}, PrivateKey: clientPK},
			},
		}))
	})

	t.Run("if client uses DNS but none are `cluster.local`, expect error", func(t *testing.T) {
		clientCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"no-cluster.foo.local", "local.cluster"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &clientPK.PublicKey, issKey)
		require.NoError(t, err)

		err = dial(t, &tls.Config{
			RootCAs:            rootPool,
			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{clientCertDER, issCert.Raw}, PrivateKey: clientPK},
			},
		})
		require.ErrorContains(t, err, "remote error: tls: bad certificate")
	})

	t.Run("if client uses DNS but is from different root, expect error", func(t *testing.T) {
		clientCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"no-cluster.foo.local", "local.cluster"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, diffCert, &clientPK.PublicKey, diffKey)
		require.NoError(t, err)

		err = dial(t, &tls.Config{
			RootCAs:            diffPool,
			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{clientCertDER, diffCert.Raw}, PrivateKey: clientPK},
			},
		})
		require.ErrorContains(t, err, "remote error: tls: bad certificate")
	})
}

func Test_NewDialClient(t *testing.T) {
	issCert, issKey, rootCA, rootPool := genIssuerCA(t)
	diffCert, diffKey, diffRootCA, diffPool := genIssuerCA(t)

	clientPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	clientPKDER, err := x509.MarshalPKCS8PrivateKey(clientPK)
	require.NoError(t, err)

	serverPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serverPKDER, err := x509.MarshalPKCS8PrivateKey(serverPK)
	require.NoError(t, err)

	clientCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		URIs:         []*url.URL{spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.com"), "client").URL()},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Minute),
		SerialNumber: serialNumber(t),
	}, issCert, &clientPK.PublicKey, issKey)
	require.NoError(t, err)

	clientSVID, err := x509svid.ParseRaw(append(clientCertDER, issCert.Raw...), clientPKDER)
	require.NoError(t, err)

	serve := func(t *testing.T, serverConfig *tls.Config) error {
		var lis net.Listener
		var lock sync.Mutex

		server := &http.Server{
			Addr:              "localhost:0",
			TLSConfig:         serverConfig,
			ReadHeaderTimeout: time.Second,
		}
		server.BaseContext = func(nlis net.Listener) context.Context {
			lock.Lock()
			defer lock.Unlock()
			lis = nlis
			return context.Background()
		}

		serverClosed := make(chan struct{})
		go func() {
			defer close(serverClosed)
			require.ErrorIs(t, server.ListenAndServeTLS("", ""), http.ErrServerClosed)
		}()

		t.Cleanup(func() {
			require.NoError(t, server.Close())
			select {
			case <-serverClosed:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for server to close")
			}
		})

		assert.Eventually(t, func() bool {
			lock.Lock()
			defer lock.Unlock()
			return lis != nil
		}, time.Second, time.Millisecond)

		client := &http.Client{Transport: &http.Transport{TLSClientConfig: NewDialClient(clientSVID, rootCA, tlsconfig.AuthorizeAny())}}
		conn, err := client.Get(fmt.Sprintf("https://localhost:%d/", lis.Addr().(*net.TCPAddr).Port))
		if err != nil {
			return err
		}
		conn.Body.Close()
		return nil
	}

	t.Run("if server uses a SVID in the same root with the correct Trust Domain, no error", func(t *testing.T) {
		id := spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.com"), "server")

		serverCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			URIs:         []*url.URL{id.URL()},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &serverPK.PublicKey, issKey)
		require.NoError(t, err)

		serversvid, err := x509svid.ParseRaw(append(serverCertDER, issCert.Raw...), serverPKDER)
		require.NoError(t, err)

		require.NoError(t, serve(t, tlsconfig.MTLSServerConfig(serversvid, rootCA, tlsconfig.AuthorizeAny())))
	})

	t.Run("if server uses a SVID but signed by different root, expect error", func(t *testing.T) {
		id := spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.com"), "server")

		serverCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			URIs:         []*url.URL{id.URL()},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, diffCert, &serverPK.PublicKey, diffKey)
		require.NoError(t, err)

		serversvid, err := x509svid.ParseRaw(append(serverCertDER, diffCert.Raw...), serverPKDER)
		require.NoError(t, err)

		err = serve(t, tlsconfig.MTLSServerConfig(serversvid, diffRootCA, tlsconfig.AuthorizeAny()))
		require.ErrorContains(t, err, "x509: ECDSA verification failure")
	})

	t.Run("if server uses DNS and is `cluster.local`, expect no error", func(t *testing.T) {
		serverCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"cluster.local"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &serverPK.PublicKey, issKey)
		require.NoError(t, err)

		require.NoError(t, serve(t, &tls.Config{
			RootCAs:            rootPool,
			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{serverCertDER, issCert.Raw}, PrivateKey: serverPK},
			},
		}))
	})

	t.Run("if server uses DNS and one is `cluster.local`, expect no error", func(t *testing.T) {
		serverCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"no-cluster.foo.local", "cluster.local"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &serverPK.PublicKey, issKey)
		require.NoError(t, err)

		require.NoError(t, serve(t, &tls.Config{
			RootCAs:            rootPool,
			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{serverCertDER, issCert.Raw}, PrivateKey: serverPK},
			},
		}))
	})

	t.Run("if server uses DNS but none are `cluster.local`, expect error", func(t *testing.T) {
		serverCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"no-cluster.foo.local", "local.cluster"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, issCert, &serverPK.PublicKey, issKey)
		require.NoError(t, err)

		err = serve(t, &tls.Config{
			RootCAs:            rootPool,
			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{serverCertDER, issCert.Raw}, PrivateKey: serverPK},
			},
		})
		require.ErrorContains(t, err, "x509svid: could not get leaf SPIFFE ID: certificate contains no URI SAN\nx509: certificate is valid for no-cluster.foo.local, local.cluster, not cluster.local")
	})

	t.Run("if server uses DNS but is from different root, expect error", func(t *testing.T) {
		serverCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			DNSNames:     []string{"no-cluster.foo.local", "local.cluster"},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Minute),
			SerialNumber: serialNumber(t),
		}, diffCert, &serverPK.PublicKey, diffKey)
		require.NoError(t, err)

		err = serve(t, &tls.Config{
			RootCAs:            diffPool,
			InsecureSkipVerify: true, //nolint: gosec // this is a test
			Certificates: []tls.Certificate{
				{Certificate: [][]byte{serverCertDER, diffCert.Raw}, PrivateKey: serverPK},
			},
		})
		require.ErrorContains(t, err, "x509svid: could not get leaf SPIFFE ID: certificate contains no URI SAN\nx509: certificate is valid for no-cluster.foo.local, local.cluster, not cluster.local")
	})
}
