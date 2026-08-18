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

package grpc

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/api/universal"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
	daprt "github.com/dapr/dapr/pkg/testing"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
	"github.com/dapr/kit/logger"
)

// fakeX509Source is a sentinel x509svid.Source. The test only checks that the
// source reference reaches the component context, so the methods are stubs.
type fakeX509Source struct{}

func (fakeX509Source) GetX509SVID() (*x509svid.SVID, error) {
	return nil, errors.New("not implemented")
}

// fakeJWTSource is a sentinel jwtsvid.Source.
type fakeJWTSource struct{}

func (fakeJWTSource) FetchJWTSVID(context.Context, jwtsvid.Params) (*jwtsvid.SVID, error) {
	return nil, errors.New("not implemented")
}

// capturingStore records the context passed to Set and Get so the test can
// assert what a building-block component observes during an operation.
type capturedContexts struct {
	mu     sync.Mutex
	setCtx context.Context
	getCtx context.Context
}

// newCapturingStore returns a mock state store that records the operation
// context, registered under "store1".
func newCapturingStore() (*daprt.MockStateStore, *capturedContexts) {
	captured := &capturedContexts{}
	store := &daprt.MockStateStore{}
	store.On("Set", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured.mu.Lock()
			captured.setCtx = args.Get(0).(context.Context)
			captured.mu.Unlock()
		}).Return(nil)
	store.On("Get", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured.mu.Lock()
			captured.getCtx = args.Get(0).(context.Context)
			captured.mu.Unlock()
		}).Return(&state.GetResponse{Data: []byte("ok")}, nil)
	return store, captured
}

// stateRoundTrip wires the real gRPC API with the given resiliency provider and
// drives a SaveState followed by a GetState against the capturing store.
func stateRoundTrip(t *testing.T, ctx context.Context, res resiliency.Provider) *capturedContexts {
	t.Helper()

	store, captured := newCapturingStore()
	compStore := compstore.New()
	compStore.AddStateStore("store1", store)

	fakeAPI := &api{
		logger: logger.NewLogger("grpc.api.test"),
		Universal: universal.New(universal.Options{
			AppID:      "fakeAPI",
			Logger:     logger.NewLogger("grpc.api.test"),
			CompStore:  compStore,
			Resiliency: res,
		}),
	}

	lis := startDaprAPIServer(t, fakeAPI, "")
	conn := createTestClient(lis)
	t.Cleanup(func() { conn.Close() })
	client := runtimev1pb.NewDaprClient(conn)

	_, err := client.SaveState(ctx, &runtimev1pb.SaveStateRequest{
		StoreName: "store1",
		States:    []*commonv1pb.StateItem{{Key: "key1", Value: []byte("value1")}},
	})
	require.NoError(t, err)

	_, err = client.GetState(ctx, &runtimev1pb.GetStateRequest{
		StoreName: "store1",
		Key:       "key1",
	})
	require.NoError(t, err)

	return captured
}

// TestComponentOperationSVIDContext is an in-process integration test that
// exercises the real request path (gRPC server -> universal API -> compStore ->
// resiliency runner -> component) and asserts that the workload's SPIFFE
// identity, injected via security.Handler.WithSVIDContext wired into the
// resiliency provider, reaches the state store's Set/Get operation context.
//
// daprd runs the component in-process, so this verifies the in-memory SVID
// sources that the cross-process integration suite (tests/integration) cannot
// observe, because context values never serialize across a process boundary.
func TestComponentOperationSVIDContext(t *testing.T) {
	t.Run("sources reach the component operation context when wired", func(t *testing.T) {
		// Mirror pkg/runtime/config.go: wire the security handler's
		// WithSVIDContext as the component context decorator. The fake handler
		// attaches the X.509 and JWT SVID sources exactly as the real handler's
		// spiffecontext.WithSpiffe does.
		sec := securityfake.New().WithSVIDContextFn(func(ctx context.Context) context.Context {
			ctx = spiffecontext.WithX509(ctx, fakeX509Source{})
			return spiffecontext.WithJWT(ctx, fakeJWTSource{})
		})
		res := resiliency.New(logger.NewLogger("grpc.api.test"))
		res.SetComponentContextDecorator(sec.WithSVIDContext)

		captured := stateRoundTrip(t, t.Context(), res)

		captured.mu.Lock()
		defer captured.mu.Unlock()

		require.NotNil(t, captured.setCtx, "Set was not called")
		_, ok := spiffecontext.X509From(captured.setCtx)
		assert.True(t, ok, "Set context should carry the X.509 SVID source")
		_, ok = spiffecontext.JWTFrom(captured.setCtx)
		assert.True(t, ok, "Set context should carry the JWT SVID source")

		require.NotNil(t, captured.getCtx, "Get was not called")
		_, ok = spiffecontext.X509From(captured.getCtx)
		assert.True(t, ok, "Get context should carry the X.509 SVID source")
		_, ok = spiffecontext.JWTFrom(captured.getCtx)
		assert.True(t, ok, "Get context should carry the JWT SVID source")
	})

	t.Run("no sources when the provider has no decorator", func(t *testing.T) {
		// Without a decorator (e.g. mTLS/SPIFFE disabled) component contexts are
		// left untouched, confirming the injection comes specifically from the
		// wiring above and nothing leaks in by default.
		res := resiliency.New(logger.NewLogger("grpc.api.test"))

		captured := stateRoundTrip(t, t.Context(), res)

		captured.mu.Lock()
		defer captured.mu.Unlock()

		require.NotNil(t, captured.setCtx, "Set was not called")
		_, ok := spiffecontext.X509From(captured.setCtx)
		assert.False(t, ok, "Set context must not carry an X.509 SVID source")
		_, ok = spiffecontext.JWTFrom(captured.setCtx)
		assert.False(t, ok, "Set context must not carry a JWT SVID source")
	})
}
