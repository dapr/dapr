/*
Copyright 2024 The Dapr Authors
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

package informer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/crypto/test"
)

// fakeCache is a minimal ctrlcache.Cache stub that only implements GetInformer.
type fakeCache struct {
	ctrlcache.Cache
	getInformerErr error
}

func (f *fakeCache) GetInformer(ctx context.Context, obj client.Object, opts ...ctrlcache.InformerGetOption) (ctrlcache.Informer, error) {
	return nil, f.getInformerErr
}

func Test_WatchUpdates(t *testing.T) {
	t.Run("bad authz should error", func(t *testing.T) {
		appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
		serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-operator")
		pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

		i := New[compapi.Component](Options{}).(*informer[compapi.Component])

		appCh, _, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns2")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, appCh)

		appCh, _, err = i.WatchUpdates(t.Context(), "ns2")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, appCh)
	})

	t.Run("should receive app events on batch events in order", func(t *testing.T) {
		appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
		serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-operator")
		pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

		i := New[compapi.Component](Options{}).(*informer[compapi.Component])

		appCh1, cancel1, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns1")
		require.NoError(t, err)
		t.Cleanup(cancel1)
		appCh2, cancel2, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns1")
		require.NoError(t, err)
		t.Cleanup(cancel2)

		i.handleEvent(t.Context(),
			&compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
			},
			&compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
				Spec:       compapi.ComponentSpec{Type: "bindings.redis"},
			},
			operator.ResourceEventType_UPDATED,
		)

		i.handleEvent(t.Context(),
			&compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
				Spec:       compapi.ComponentSpec{Type: "bindings.redis"},
			},
			&compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
				Scoped:     common.Scoped{Scopes: []string{"notapp1"}},
			},
			operator.ResourceEventType_UPDATED,
		)

		i.handleEvent(t.Context(),
			nil,
			&compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp2", Namespace: "ns1"},
			},
			operator.ResourceEventType_CREATED,
		)

		for _, appCh := range []<-chan *Event[compapi.Component]{appCh1, appCh2} {
			for _, exp := range []*Event[compapi.Component]{
				{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
						Spec:       compapi.ComponentSpec{Type: "bindings.redis"},
					},
					Type: operator.ResourceEventType_UPDATED,
				},
				{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
						Spec:       compapi.ComponentSpec{Type: "bindings.redis"},
					},
					Type: operator.ResourceEventType_DELETED,
				},
				{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "comp2", Namespace: "ns1"},
					},
					Type: operator.ResourceEventType_CREATED,
				},
			} {
				select {
				case event := <-appCh:
					assert.Equal(t, exp, event)
				case <-time.After(time.Second):
					assert.Fail(t, "timeout waiting for app event")
				}
			}
		}
	})

	t.Run("once shutting down, WatchUpdates is rejected and cannot leak a watcher", func(t *testing.T) {
		appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
		serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-operator")
		pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

		i := New[compapi.Component](Options{
			Cache: &fakeCache{
				getInformerErr: &apimeta.NoKindMatchError{
					GroupKind:        schema.GroupKind{Group: "dapr.io", Kind: "Component"},
					SearchedVersions: []string{"v1alpha1"},
				},
			},
		}).(*informer[compapi.Component])

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() { errCh <- i.Run(ctx) }()

		// A watcher registered while running is closed when Run shuts down.
		appCh, _, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns1")
		require.NoError(t, err)

		cancel()
		select {
		case runErr := <-errCh:
			require.NoError(t, runErr)
		case <-time.After(time.Second):
			t.Fatal("expected Run to return after context cancellation")
		}

		// The existing watcher's channel was closed by Run.
		select {
		case _, ok := <-appCh:
			assert.False(t, ok, "expected watcher channel to be closed on shutdown")
		case <-time.After(time.Second):
			assert.Fail(t, "expected watcher channel to be closed on shutdown")
		}

		// A WatchUpdates that races in after shutdown is rejected rather than
		// registering a watcher that nobody would ever close.
		appCh, watchCancel, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns1")
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
		assert.Nil(t, appCh)
		assert.Nil(t, watchCancel)

		i.lock.Lock()
		assert.Empty(t, i.watchers, "no watcher should be registered after shutdown")
		i.lock.Unlock()
	})
}

func Test_handleEvent_resyncDedup(t *testing.T) {
	appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-operator")
	pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

	comp := func(rv string, typ string) *compapi.Component {
		return &compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1", ResourceVersion: rv},
			Spec:       compapi.ComponentSpec{Type: typ},
		}
	}

	tests := map[string]struct {
		old       *compapi.Component
		new       *compapi.Component
		eventType operator.ResourceEventType
		expFwd    bool
	}{
		"UPDATED with equal non-empty resourceVersion is dropped (resync replay)": {
			old:       comp("100", "bindings.redis"),
			new:       comp("100", "bindings.redis"),
			eventType: operator.ResourceEventType_UPDATED,
			expFwd:    false,
		},
		"UPDATED with changed resourceVersion is forwarded": {
			old:       comp("100", "bindings.redis"),
			new:       comp("101", "bindings.kafka"),
			eventType: operator.ResourceEventType_UPDATED,
			expFwd:    true,
		},
		"UPDATED with empty resourceVersions is forwarded (back-compat)": {
			old:       comp("", "bindings.redis"),
			new:       comp("", "bindings.redis"),
			eventType: operator.ResourceEventType_UPDATED,
			expFwd:    true,
		},
		"CREATED with equal resourceVersion is forwarded (only UPDATED is deduped)": {
			old:       nil,
			new:       comp("100", "bindings.redis"),
			eventType: operator.ResourceEventType_CREATED,
			expFwd:    true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			i := New[compapi.Component](Options{}).(*informer[compapi.Component])

			appCh, cancel, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns1")
			require.NoError(t, err)
			t.Cleanup(cancel)

			var oldObj any
			if test.old != nil {
				oldObj = test.old
			}
			i.handleEvent(t.Context(), oldObj, test.new, test.eventType)

			select {
			case event := <-appCh:
				assert.True(t, test.expFwd, "event was forwarded but expected it to be dropped")
				assert.Equal(t, test.new.Spec.Type, event.Manifest.Spec.Type)
			case <-time.After(500 * time.Millisecond):
				assert.False(t, test.expFwd, "event was dropped but expected it to be forwarded")
			}
		})
	}
}

func Test_Run(t *testing.T) {
	t.Run("NoKindMatchError should not return error", func(t *testing.T) {
		i := New[compapi.Component](Options{
			Cache: &fakeCache{
				getInformerErr: &apimeta.NoKindMatchError{
					GroupKind:        schema.GroupKind{Group: "dapr.io", Kind: "Component"},
					SearchedVersions: []string{"v1alpha1"},
				},
			},
		})

		ctx, cancel := context.WithCancel(t.Context())

		errCh := make(chan error, 1)
		go func() {
			errCh <- i.Run(ctx)
		}()

		// Ensure Run blocks (does not return immediately).
		select {
		case err := <-errCh:
			t.Fatalf("expected Run to block, but it returned: %v", err)
		case <-time.After(100 * time.Millisecond):
		}

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("expected Run to return after context cancellation")
		}
	})

	t.Run("other errors should be returned", func(t *testing.T) {
		i := New[compapi.Component](Options{
			Cache: &fakeCache{
				getInformerErr: errors.New("some other error"),
			},
		})

		err := i.Run(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to get setup Component informer")
		assert.Contains(t, err.Error(), "some other error")
	})
}

func Test_anyToT(t *testing.T) {
	tests := map[string]struct {
		obj   any
		expT  compapi.Component
		expOK bool
	}{
		"type *T ok": {
			obj:   new(compapi.Component),
			expT:  compapi.Component{},
			expOK: true,
		},
		"type T ok": {
			obj:   compapi.Component{},
			expT:  compapi.Component{},
			expOK: true,
		},
		"type cache.DeletedFinalStateUnknown(*T) ok": {
			obj:   cache.DeletedFinalStateUnknown{Obj: new(compapi.Component)},
			expT:  compapi.Component{},
			expOK: true,
		},
		"type cache.DeletedFinalStateUnknown(T) ok": {
			obj:   cache.DeletedFinalStateUnknown{Obj: compapi.Component{}},
			expT:  compapi.Component{},
			expOK: true,
		},
		"type cache.DeletedFinalStateUnknown(not_T) not ok": {
			obj:   cache.DeletedFinalStateUnknown{Obj: new(subapi.Subscription)},
			expT:  compapi.Component{},
			expOK: false,
		},
		"type different, not ok": {
			obj:   new(subapi.Subscription),
			expT:  compapi.Component{},
			expOK: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			i := New[compapi.Component](Options{}).(*informer[compapi.Component])

			got, ok := i.anyToT(test.obj)
			assert.Equal(t, test.expOK, ok)
			assert.Equal(t, test.expT, got)
		})
	}
}
