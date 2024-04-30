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
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/util"
)

func Test_WatchUpdates(t *testing.T) {
	t.Run("bad authz should error", func(t *testing.T) {
		appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
		serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-operator")
		pki := util.GenPKI(t, util.PKIOptions{LeafID: serverID, ClientID: appID})

		i := New[compapi.Component](Options{}).(*informer[compapi.Component])

		appCh, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns2")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, appCh)

		appCh, err = i.WatchUpdates(context.Background(), "ns2")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, appCh)
	})

	t.Run("should receive app events on batch events in order", func(t *testing.T) {
		appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
		serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-operator")
		pki := util.GenPKI(t, util.PKIOptions{LeafID: serverID, ClientID: appID})

		i := New[compapi.Component](Options{}).(*informer[compapi.Component])
		t.Cleanup(func() { close(i.closeCh) })

		appCh1, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns1")
		require.NoError(t, err)
		appCh2, err := i.WatchUpdates(pki.ClientGRPCCtx(t), "ns1")
		require.NoError(t, err)

		i.handleEvent(context.Background(),
			&compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
			},
			&compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "ns1"},
				Spec:       compapi.ComponentSpec{Type: "bindings.redis"},
			},
			operator.ResourceEventType_UPDATED,
		)
		i.handleEvent(context.Background(),
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
		i.handleEvent(context.Background(),
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
