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

package secret

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/secretstores"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/security"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
	"github.com/dapr/kit/logger"
)

// fakeX509Source is a sentinel x509svid.Source. The test only checks that the
// source reference reaches the secret store, so the methods are stubs.
type fakeX509Source struct{}

func (fakeX509Source) GetX509SVID() (*x509svid.SVID, error) {
	return nil, errors.New("not implemented")
}

// fakeJWTSource is a sentinel jwtsvid.Source.
type fakeJWTSource struct{}

func (fakeJWTSource) FetchJWTSVID(context.Context, jwtsvid.Params) (*jwtsvid.SVID, error) {
	return nil, errors.New("not implemented")
}

// capturingSecretStore records the context that GetSecret was invoked with, so
// a test can assert what the store observes during secret resolution.
type capturingSecretStore struct {
	secretstores.SecretStore

	lock sync.Mutex
	ctx  context.Context
}

func (c *capturingSecretStore) Init(context.Context, secretstores.Metadata) error {
	return nil
}

func (c *capturingSecretStore) GetSecret(ctx context.Context, _ secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.ctx = ctx

	return secretstores.GetSecretResponse{Data: map[string]string{"key1": "value1"}}, nil
}

func (c *capturingSecretStore) operationContext(t *testing.T) context.Context {
	t.Helper()

	c.lock.Lock()
	defer c.lock.Unlock()

	require.NotNil(t, c.ctx, "GetSecret was not called")

	return c.ctx
}

// TestProcessResourceSVIDContext asserts that resolving a secretKeyRef calls
// the referenced secret store with the workload's SPIFFE identity attached.
// Resolution happens before a component is initialised, on a context that the
// caller (the root loop, the http endpoint and MCP server loops, or the hot
// reload reconciler) has not decorated, so the sub-processor must attach it.
func TestProcessResourceSVIDContext(t *testing.T) {
	newProbe := func(t *testing.T, sec security.Handler) (*secret, *capturingSecretStore) {
		t.Helper()

		store := new(capturingSecretStore)

		s := New(Options{
			Registry:       registry.New(registry.NewOptions()).SecretStores(),
			ComponentStore: compstore.New(),
			Meta: meta.New(meta.Options{
				ID:   "test",
				Mode: modes.StandaloneMode,
			}),
			Security: sec,
		})

		s.registry.RegisterComponent(
			func(logger.Logger) secretstores.SecretStore { return store },
			"svidprobe",
		)

		require.NoError(t, s.Init(t.Context(), componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "svidprobe-store"},
			Spec: componentsapi.ComponentSpec{
				Type:    "secretstores.svidprobe",
				Version: "v1",
			},
		}))

		return s, store
	}

	// A resource whose metadata value must be resolved from svidprobe-store.
	newResource := func() *componentsapi.Component {
		comp := &componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "mockBinding"},
			Spec: componentsapi.ComponentSpec{
				Type:    "bindings.mock",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{{
					Name: "a",
					SecretKeyRef: commonapi.SecretKeyRef{
						Key:  "key1",
						Name: "name1",
					},
				}},
			},
		}
		comp.SecretStore = "svidprobe-store"
		return comp
	}

	t.Run("the secret store sees the SVID sources when security is wired", func(t *testing.T) {
		// The fake attaches the X.509 and JWT SVID sources exactly as the real
		// handler's spiffecontext.WithSpiffe does when mTLS is enabled.
		sec := securityfake.New().WithSVIDContextFn(func(ctx context.Context) context.Context {
			ctx = spiffecontext.WithX509(ctx, fakeX509Source{})
			return spiffecontext.WithJWT(ctx, fakeJWTSource{})
		})

		s, store := newProbe(t, sec)

		comp := newResource()
		updated, unready := s.ProcessResource(t.Context(), comp)
		require.True(t, updated)
		require.Empty(t, unready)
		assert.Equal(t, "value1", comp.Spec.Metadata[0].Value.String())

		ctx := store.operationContext(t)
		_, ok := spiffecontext.X509From(ctx)
		assert.True(t, ok, "GetSecret context should carry the X.509 SVID source")
		_, ok = spiffecontext.JWTFrom(ctx)
		assert.True(t, ok, "GetSecret context should carry the JWT SVID source")
	})

	t.Run("the secret store sees no SVID sources when mTLS is disabled", func(t *testing.T) {
		// WithSVIDContext returns ctx untouched when SPIFFE is not configured
		// (the fake's default). Negative control proving the assertion above is
		// not vacuous: nothing leaks into the context by default.
		s, store := newProbe(t, securityfake.New())

		comp := newResource()
		updated, unready := s.ProcessResource(t.Context(), comp)
		require.True(t, updated)
		require.Empty(t, unready)

		ctx := store.operationContext(t)
		_, ok := spiffecontext.X509From(ctx)
		assert.False(t, ok, "X.509 SVID source should be absent when mTLS is disabled")
		_, ok = spiffecontext.JWTFrom(ctx)
		assert.False(t, ok, "JWT SVID source should be absent when mTLS is disabled")
	})

	t.Run("no security handler is not fatal", func(t *testing.T) {
		// Unit tests construct the sub-processor without a handler; resolution
		// must still work rather than nil panic.
		s, store := newProbe(t, nil)

		comp := newResource()
		updated, unready := s.ProcessResource(t.Context(), comp)
		require.True(t, updated)
		require.Empty(t, unready)
		assert.Equal(t, "value1", comp.Spec.Metadata[0].Value.String())

		_, ok := spiffecontext.X509From(store.operationContext(t))
		assert.False(t, ok)
	})
}
