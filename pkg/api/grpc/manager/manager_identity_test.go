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

package manager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	kclock "k8s.io/utils/clock"
	testingclock "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/crypto/spiffe"
)

const (
	identityTestTrustDomain = "public"
	testAddr                = "h:1"
)

type identityTestCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newIdentityTestCA(t *testing.T) *identityTestCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "identity-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return &identityTestCA{
		cert: cert,
		key:  key,
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// handler returns the real security.Handler (real SPIFFE matcher and
// go-spiffe credentials) for the app `appID` in `ns`. Only the Sentry round
// trip is replaced: the CSR is signed by the throwaway CA.
func (ca *identityTestCA) handler(t *testing.T, ns, appID string) security.Handler {
	t.Helper()

	sec, err := security.New(t.Context(), security.Options{
		AppID:                   appID,
		ControlPlaneTrustDomain: identityTestTrustDomain,
		ControlPlaneNamespace:   "dapr-system",
		MTLSEnabled:             true,
		TrustAnchors:            ca.pem,
		Healthz:                 healthz.New(),
		OverrideCertRequestFn: func(_ context.Context, csrDER []byte) (*spiffe.SVIDResponse, error) {
			csr, cerr := x509.ParseCertificateRequest(csrDER)
			if cerr != nil {
				return nil, cerr
			}
			id := spiffeid.RequireFromPath(
				spiffeid.RequireTrustDomainFromString(identityTestTrustDomain),
				"/ns/"+ns+"/"+appID,
			)
			tmpl := &x509.Certificate{
				SerialNumber: big.NewInt(time.Now().UnixNano()),
				NotBefore:    time.Now().Add(-time.Minute),
				NotAfter:     time.Now().Add(time.Hour),
				KeyUsage:     x509.KeyUsageDigitalSignature,
				ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
				URIs:         []*url.URL{id.URL()},
			}
			der, cerr := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, csr.PublicKey, ca.key)
			if cerr != nil {
				return nil, cerr
			}
			cert, cerr := x509.ParseCertificate(der)
			if cerr != nil {
				return nil, cerr
			}
			return &spiffe.SVIDResponse{X509Certificates: []*x509.Certificate{cert}}, nil
		},
	})
	require.NoError(t, err)
	go func() { _ = sec.Run(t.Context()) }()
	h, err := sec.Handler(t.Context())
	require.NoError(t, err)

	return h
}

// chanListener lets a grpc.Server be stopped (Stop closes the listener)
// without closing the real TCP listener, so the port is never re-bound.
type chanListener struct {
	net.Listener
	ch   <-chan net.Conn
	done chan struct{}
	once sync.Once
}

func (c *chanListener) Accept() (net.Conn, error) {
	select {
	case conn := <-c.ch:
		return conn, nil
	case <-c.done:
		return nil, net.ErrClosed
	}
}

func (c *chanListener) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

// identityServer is one real TCP address whose serving app can change over
// time, simulating a pod IP that is reused by a different app.
type identityServer struct {
	real net.Listener
	ch   chan net.Conn
	srv  *grpc.Server
}

func newIdentityServer(t *testing.T) *identityServer {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &identityServer{real: l, ch: make(chan net.Conn)}
	done := make(chan struct{})
	go func() {
		for {
			c, aerr := l.Accept()
			if aerr != nil {
				return
			}
			select {
			case s.ch <- c:
			case <-done:
				_ = c.Close()
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(done)
		_ = l.Close()
		s.stop()
	})

	return s
}

func (s *identityServer) address() string {
	return s.real.Addr().String()
}

func (s *identityServer) stop() {
	if s.srv != nil {
		s.srv.Stop()
		s.srv = nil
	}
}

// serveAs makes the address be served by the app owning h.
func (s *identityServer) serveAs(h security.Handler) {
	s.stop()
	srv := grpc.NewServer(h.GRPCServerOptionMTLS())
	healthpb.RegisterHealthServer(srv, health.NewServer())
	s.srv = srv
	go srv.Serve(&chanListener{Listener: s.real, ch: s.ch, done: make(chan struct{})})
}

// newIdentityManager returns a Manager for app "a" in ns that closes every
// pooled connection when the test ends, so no ClientConn keeps redialing.
func newIdentityManager(t *testing.T, ca *identityTestCA, ns string) *Manager {
	t.Helper()

	mgr := NewManager(ca.handler(t, ns, "a"), modes.StandaloneMode, &AppChannelConfig{})
	t.Cleanup(func() {
		mgr.remoteConns.pool.Range(func(_, p any) bool {
			p.(*ConnectionPool).DestroyAll()
			return true
		})
		_ = mgr.Close()
	})

	return mgr
}

func checkIdentityConn(ctx context.Context, conn *grpc.ClientConn) error {
	return conn.Invoke(ctx, healthpb.Health_Check_FullMethodName, &healthpb.HealthCheckRequest{}, &healthpb.HealthCheckResponse{})
}

func TestGetGRPCConnection_ReusedAddressNewIdentity(t *testing.T) {
	const ns = "ns"
	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	// The address belongs to app C and its connection is pooled.
	srv.serveAs(ca.handler(t, ns, "c"))
	connC, releaseC, err := mgr.GetGRPCConnection(ctx, srv.address(), "c", ns)
	require.NoError(t, err)
	require.NoError(t, checkIdentityConn(ctx, connC))
	releaseC(false)

	// The same address is now served by app B (pod IP reuse).
	srv.serveAs(ca.handler(t, ns, "b"))
	connB, releaseB, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	defer releaseB(false)

	assert.NotSame(t, connC, connB, "a different identity must not reuse the pooled connection")
	require.NoError(t, checkIdentityConn(ctx, connB), "the first call to the new identity must succeed")
}

func TestGetGRPCConnection_NamespaceInKey(t *testing.T) {
	const ns = "ns"
	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	srv.serveAs(ca.handler(t, ns, "b"))
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	// Wrong namespace first: the handshake must be rejected by the matcher.
	connOther, releaseOther, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", "other")
	require.NoError(t, err)
	defer releaseOther(false)
	err = checkIdentityConn(ctx, connOther)
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Contains(t, err.Error(), "unexpected SPIFFE ID")

	// Same app ID and address in the right namespace must not reuse that connection.
	connNS, releaseNS, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	defer releaseNS(false)

	assert.NotSame(t, connOther, connNS, "a different namespace must not reuse the pooled connection")
	require.NoError(t, checkIdentityConn(ctx, connNS))
}

func TestGetGRPCConnection_SameIdentityShares(t *testing.T) {
	const ns = "ns"
	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	srv.serveAs(ca.handler(t, ns, "b"))
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	conn1, release1, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	defer release1(false)
	conn2, release2, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	defer release2(false)

	assert.Same(t, conn1, conn2, "the same identity must keep sharing one connection")
	assert.NoError(t, checkIdentityConn(ctx, conn1))
}

func TestGetGRPCConnection_DestroyUsesKey(t *testing.T) {
	const ns = "ns"
	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	srv.serveAs(ca.handler(t, ns, "b"))
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	conn1, teardown1, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	require.NoError(t, checkIdentityConn(ctx, conn1))
	teardown1(true)
	assert.Equal(t, connectivity.Shutdown.String(), conn1.GetState().String(), "destroy must close the connection it was handed")

	conn2, teardown2, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	defer teardown2(false)
	assert.NotSame(t, conn1, conn2, "a destroyed connection must not be handed out again")
	assert.NoError(t, checkIdentityConn(ctx, conn2))
}

func TestGetGRPCConnection_StaleIdentityPurged(t *testing.T) {
	const ns = "ns"
	// Mock time so idle expiry is deterministic; no t.Parallel with the swap.
	clockMock := testingclock.NewFakeClock(time.Now())
	clock = clockMock
	t.Cleanup(func() { clock = &kclock.RealClock{} })

	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	srv.serveAs(ca.handler(t, ns, "c"))
	connC, releaseC, err := mgr.GetGRPCConnection(ctx, srv.address(), "c", ns)
	require.NoError(t, err)
	require.NoError(t, checkIdentityConn(ctx, connC))

	// C stays held while B is dialed, so only the idle purge can close it.
	srv.serveAs(ca.handler(t, ns, "b"))
	connB, releaseB, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	releaseC(false)
	releaseB(false)

	clockMock.Step(maxConnIdle + time.Second)
	mgr.remoteConns.Purge()

	assert.Equal(t, connectivity.Shutdown.String(), connC.GetState().String(), "the orphaned identity's connection must be purged")
	assert.Equal(t, connectivity.Shutdown.String(), connB.GetState().String(), "the current identity's connection must be purged once idle")
}

func TestGetGRPCConnection_KeyFieldsAreSeparated(t *testing.T) {
	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	srv.serveAs(ca.handler(t, "ns", "b"))
	mgr := newIdentityManager(t, ca, "ns")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	// (namespace "n", app "sb") and (namespace "ns", app "b") concatenate to the
	// same string; the wrong one is dialed first and must not be reused.
	connWrong, releaseWrong, err := mgr.GetGRPCConnection(ctx, srv.address(), "sb", "n")
	require.NoError(t, err)
	defer releaseWrong(false)
	err = checkIdentityConn(ctx, connWrong)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected SPIFFE ID")

	connRight, releaseRight, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", "ns")
	require.NoError(t, err)
	defer releaseRight(false)

	assert.NotSame(t, connWrong, connRight, "key fields must be separated so distinct identities never collide")
	require.NoError(t, checkIdentityConn(ctx, connRight))
}

// Addresses are host:port (no "/"). Distinct (address, namespace, app ID)
// tuples must give distinct keys when no field contains "/"; a "/" inside
// namespace or app ID can merge keys, but only for tuples whose expected
// SPIFFE path (/ns/<namespace>/<appID>) is identical too.
func TestRemoteConnKey_Injective(t *testing.T) {
	tests := map[string][2][3]string{
		"address digits move into namespace": {{"h:5000", "1x", "b"}, {"h:50001", "x", "b"}},
		"namespace suffix moves into app ID": {{testAddr, "n", "sb"}, {testAddr, "ns", "b"}},
		"same app ID, other namespace":       {{testAddr, "ns1", "b"}, {testAddr, "ns2", "b"}},
		"same app ID, empty namespace":       {{testAddr, "", "b"}, {testAddr, "ns", "b"}},
		"same namespace, other app ID":       {{testAddr, "ns", "b"}, {testAddr, "ns", "c"}},
		"same identity, other address":       {{testAddr, "ns", "b"}, {"h:2", "ns", "b"}},
		"empty field swapped":                {{testAddr, "", "x"}, {testAddr, "x", ""}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a, b := tc[0], tc[1]
			assert.NotEqual(t, remoteConnKey(a[0], a[1], a[2]), remoteConnKey(b[0], b[1], b[2]))
		})
	}
}

// Destroy by one identity must not close the connection that another identity
// at the same address obtained concurrently.
func TestGetGRPCConnection_DestroyIsolatedPerIdentity(t *testing.T) {
	mgr := newIdentityManager(t, newIdentityTestCA(t), "ns")
	// Never served: dialing is non-blocking, the connections only have to exist.
	const addr = "127.0.0.1:1"
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	for range 200 {
		var (
			connB    *grpc.ClientConn
			releaseB func(bool)
			errB     error
			wg       sync.WaitGroup
		)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			connB, releaseB, errB = mgr.GetGRPCConnection(ctx, addr, "b", "ns")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, destroyC, err := mgr.GetGRPCConnection(ctx, addr, "c", "ns")
			if assert.NoError(t, err) {
				destroyC(true)
			}
		}()
		close(start)
		wg.Wait()

		require.NoError(t, errB)
		require.NotEqual(t, connectivity.Shutdown.String(), connB.GetState().String(), "destroying identity c closed identity b's connection")
		releaseB(false)
	}
}

func TestGetGRPCConnection_NewIdentityClosesIdleOldIdentity(t *testing.T) {
	const ns = "ns"
	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	srv.serveAs(ca.handler(t, ns, "c"))
	connC, releaseC, err := mgr.GetGRPCConnection(ctx, srv.address(), "c", ns)
	require.NoError(t, err)
	require.NoError(t, checkIdentityConn(ctx, connC))
	releaseC(false)

	srv.serveAs(ca.handler(t, ns, "b"))
	connB, releaseB, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	require.NoError(t, checkIdentityConn(ctx, connB))
	releaseB(false)

	assert.Equal(t, connectivity.Shutdown.String(), connC.GetState().String(), "the previous identity's idle connection must be closed when the address goes to another app")

	// The previous app gets the address back: it dials afresh instead of
	// waiting on the reconnect backoff of the connection that was closed.
	srv.serveAs(ca.handler(t, ns, "c"))
	connC2, releaseC2, err := mgr.GetGRPCConnection(ctx, srv.address(), "c", ns)
	require.NoError(t, err)
	defer releaseC2(false)
	assert.NotSame(t, connC, connC2)
	require.NoError(t, checkIdentityConn(ctx, connC2))
	assert.Equal(t, connectivity.Shutdown.String(), connB.GetState().String(), "the idle connection of the app that left must be closed in turn")
}

func TestGetGRPCConnection_NewIdentityKeepsHeldOldIdentity(t *testing.T) {
	const ns = "ns"
	ca := newIdentityTestCA(t)
	srv := newIdentityServer(t)
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	srv.serveAs(ca.handler(t, ns, "c"))
	connC, releaseC, err := mgr.GetGRPCConnection(ctx, srv.address(), "c", ns)
	require.NoError(t, err)
	require.NoError(t, checkIdentityConn(ctx, connC))

	// A caller still holds C's connection, as for an in-flight call.
	srv.serveAs(ca.handler(t, ns, "b"))
	_, releaseB, err := mgr.GetGRPCConnection(ctx, srv.address(), "b", ns)
	require.NoError(t, err)
	defer releaseB(false)

	assert.NotEqual(t, connectivity.Shutdown.String(), connC.GetState().String(), "a connection a caller still holds must not be closed")
	releaseC(false)
}

func TestGetGRPCConnection_NewIdentityKeepsOtherAddress(t *testing.T) {
	const ns = "ns"
	ca := newIdentityTestCA(t)
	srvC := newIdentityServer(t)
	srvB := newIdentityServer(t)
	mgr := newIdentityManager(t, ca, ns)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	srvC.serveAs(ca.handler(t, ns, "c"))
	connC, releaseC, err := mgr.GetGRPCConnection(ctx, srvC.address(), "c", ns)
	require.NoError(t, err)
	require.NoError(t, checkIdentityConn(ctx, connC))
	releaseC(false)

	srvB.serveAs(ca.handler(t, ns, "b"))
	_, releaseB, err := mgr.GetGRPCConnection(ctx, srvB.address(), "b", ns)
	require.NoError(t, err)
	defer releaseB(false)

	assert.NotEqual(t, connectivity.Shutdown.String(), connC.GetState().String(), "a connection to another address must not be closed")
	assert.NoError(t, checkIdentityConn(ctx, connC))
}

// An address that merely starts with another one (":1" and ":10") is another
// address, so its connections must not be touched.
func TestGetGRPCConnection_NewIdentityKeepsAddressSharingPrefix(t *testing.T) {
	mgr := newIdentityManager(t, newIdentityTestCA(t), "ns")
	// Never served: dialing is non-blocking, the connections only have to exist.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	connC, releaseC, err := mgr.GetGRPCConnection(ctx, "127.0.0.1:10", "c", "ns")
	require.NoError(t, err)
	releaseC(false)

	_, releaseB, err := mgr.GetGRPCConnection(ctx, "127.0.0.1:1", "b", "ns")
	require.NoError(t, err)
	defer releaseB(false)

	assert.NotEqual(t, connectivity.Shutdown.String(), connC.GetState().String(), "a connection to a different address must not be closed")
}

// Only dialing a new connection closes the idle connections of other
// identities. Reusing the pooled one must not, or two identities that use one
// address (for example with mTLS off) would close each other's connections.
func TestGetGRPCConnection_SharedHitDoesNotCloseOtherIdentities(t *testing.T) {
	mgr := newIdentityManager(t, newIdentityTestCA(t), "ns")
	const addr = "127.0.0.1:1"
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	_, releaseC, err := mgr.GetGRPCConnection(ctx, addr, "c", "ns")
	require.NoError(t, err)
	releaseC(false)

	// B is dialed: C is idle, so it is closed.
	connB, releaseB, err := mgr.GetGRPCConnection(ctx, addr, "b", "ns")
	require.NoError(t, err)

	// C is dialed again while B is held, then goes idle.
	connC2, releaseC2, err := mgr.GetGRPCConnection(ctx, addr, "c", "ns")
	require.NoError(t, err)
	releaseC2(false)

	// B's pooled connection is shared, not dialed, so C's idle one stays.
	connB2, releaseB2, err := mgr.GetGRPCConnection(ctx, addr, "b", "ns")
	require.NoError(t, err)
	defer releaseB2(false)
	defer releaseB(false)

	assert.Same(t, connB, connB2)
	assert.NotEqual(t, connectivity.Shutdown.String(), connC2.GetState().String(), "reusing a pooled connection must not close another identity's")
}
