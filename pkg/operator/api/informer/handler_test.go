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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security/spiffe"
)

func Test_loop(t *testing.T) {
	t.Run("if context is done, return", func(t *testing.T) {
		done := make(chan struct{})
		h := &handler[compapi.Component]{
			i:       &informer[compapi.Component]{closeCh: make(chan struct{})},
			batchCh: make(chan *informerEvent[compapi.Component]),
			appCh:   make(chan *Event[compapi.Component]),
		}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			h.loop(ctx)
			close(done)
		}()

		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			assert.Fail(t, "expected loop to return")
		}
	})

	t.Run("if closeCh is closed, return", func(t *testing.T) {
		done := make(chan struct{})
		closeCh := make(chan struct{})
		h := &handler[compapi.Component]{
			i:       &informer[compapi.Component]{closeCh: closeCh},
			batchCh: make(chan *informerEvent[compapi.Component]),
			appCh:   make(chan *Event[compapi.Component]),
		}

		go func() {
			h.loop(context.Background())
			close(done)
		}()

		close(closeCh)
		select {
		case <-done:
		case <-time.After(time.Second):
			assert.Fail(t, "expected loop to return")
		}
	})

	t.Run("expect to receive events in order until close", func(t *testing.T) {
		done := make(chan struct{})
		closeCh := make(chan struct{})
		batchCh := make(chan *informerEvent[compapi.Component], 10)
		appCh := make(chan *Event[compapi.Component], 10)

		td, err := spiffeid.TrustDomainFromString("example.org")
		require.NoError(t, err)
		id, err := spiffe.FromStrings(td, "myns", "myapp")
		require.NoError(t, err)

		h := &handler[compapi.Component]{
			i:       &informer[compapi.Component]{closeCh: closeCh},
			batchCh: batchCh,
			appCh:   appCh,
			id:      id,
		}

		go func() {
			h.loop(context.Background())
			close(done)
		}()

		batchCh <- &informerEvent[compapi.Component]{
			newObj: &Event[compapi.Component]{
				Manifest: compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"}},
				Type:     operatorv1.ResourceEventType_CREATED,
			},
		}
		batchCh <- &informerEvent[compapi.Component]{
			newObj: &Event[compapi.Component]{
				Manifest: compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "comp2", Namespace: "notmyns"}},
				Type:     operatorv1.ResourceEventType_CREATED,
			},
		}
		batchCh <- &informerEvent[compapi.Component]{
			newObj: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp3", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
				},
				Type: operatorv1.ResourceEventType_CREATED,
			},
		}
		batchCh <- &informerEvent[compapi.Component]{
			oldObj: &compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
			},
			newObj: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_UPDATED,
			},
		}
		batchCh <- &informerEvent[compapi.Component]{
			oldObj: &compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
				Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
			},
			newObj: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
				},
				Type: operatorv1.ResourceEventType_UPDATED,
			},
		}
		batchCh <- &informerEvent[compapi.Component]{
			oldObj: &compapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
				Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
			},
			newObj: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_UPDATED,
			},
		}
		batchCh <- &informerEvent[compapi.Component]{
			newObj: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_DELETED,
			},
		}

		for _, exp := range []*Event[compapi.Component]{
			{
				Manifest: compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"}},
				Type:     operatorv1.ResourceEventType_CREATED,
			},
			{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_UPDATED,
			},
			{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_DELETED,
			},
			{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_CREATED,
			},
			{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_DELETED,
			},
		} {
			select {
			case actual := <-appCh:
				assert.Equal(t, exp, actual)
			case <-time.After(time.Second):
				assert.Fail(t, "expected to receive event")
			}
		}

		close(closeCh)
		select {
		case <-done:
		case <-time.After(time.Second):
			assert.Fail(t, "expected loop to return")
		}
	})
}

func Test_appEventFromEvent(t *testing.T) {
	tests := map[string]struct {
		event    *informerEvent[compapi.Component]
		expEvent *Event[compapi.Component]
		expOK    bool
	}{
		"if manifest in different namespace, return false": {
			event: &informerEvent[compapi.Component]{
				oldObj: &compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
				},
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "notmyns"},
					},
					Type: operatorv1.ResourceEventType_UPDATED,
				},
			},
			expEvent: nil,
			expOK:    false,
		},
		"if manifest in same namespace, return event": {
			event: &informerEvent[compapi.Component]{
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"}},
					Type:     operatorv1.ResourceEventType_CREATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"}},
				Type:     operatorv1.ResourceEventType_CREATED,
			},
			expOK: true,
		},
		"if manifest in scope, return event": {
			event: &informerEvent[compapi.Component]{
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
						Scoped:     common.Scoped{Scopes: []string{"anotherappid", "myapp"}},
					},
					Type: operatorv1.ResourceEventType_CREATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"anotherappid", "myapp"}},
				},
				Type: operatorv1.ResourceEventType_CREATED,
			},
			expOK: true,
		},
		"if manifest not in scope, don't return event": {
			event: &informerEvent[compapi.Component]{
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
						Scoped:     common.Scoped{Scopes: []string{"anotherappid", "notmyapp"}},
					},
					Type: operatorv1.ResourceEventType_CREATED,
				},
			},
			expEvent: nil,
			expOK:    false,
		},
		"if manifest in both manifests, return new object": {
			event: &informerEvent[compapi.Component]{
				oldObj: &compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"myapp"}},
				},
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
						Scoped:     common.Scoped{Scopes: []string{"myapp", "notmyapp"}},
					},
					Type: operatorv1.ResourceEventType_UPDATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"myapp", "notmyapp"}},
				},
				Type: operatorv1.ResourceEventType_UPDATED,
			},
			expOK: true,
		},
		"if manifest in old scope but not new, return delete event": {
			event: &informerEvent[compapi.Component]{
				oldObj: &compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"myapp"}},
				},
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
						Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
					},
					Type: operatorv1.ResourceEventType_UPDATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"myapp"}},
				},
				Type: operatorv1.ResourceEventType_DELETED,
			},
			expOK: true,
		},
		"if manifest in both manifests (empty), return new object": {
			event: &informerEvent[compapi.Component]{
				oldObj: &compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"myapp"}},
				},
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					},
					Type: operatorv1.ResourceEventType_UPDATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
				},
				Type: operatorv1.ResourceEventType_UPDATED,
			},
			expOK: true,
		},
		"if manifest in old scope but not new (empty), return delete event": {
			event: &informerEvent[compapi.Component]{
				oldObj: &compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
				},
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
						Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
					},
					Type: operatorv1.ResourceEventType_UPDATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
				},
				Type: operatorv1.ResourceEventType_DELETED,
			},
			expOK: true,
		},
		"if manifest not in old but in new scope, return created": {
			event: &informerEvent[compapi.Component]{
				oldObj: &compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
				},
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
						Scoped:     common.Scoped{Scopes: []string{"myapp", "notmyapp"}},
					},
					Type: operatorv1.ResourceEventType_UPDATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"myapp", "notmyapp"}},
				},
				Type: operatorv1.ResourceEventType_CREATED,
			},
			expOK: true,
		},
		"if manifest not in old but in new scope (empty), return created": {
			event: &informerEvent[compapi.Component]{
				oldObj: &compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					Scoped:     common.Scoped{Scopes: []string{"notmyapp"}},
				},
				newObj: &Event[compapi.Component]{
					Manifest: compapi.Component{
						ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
					},
					Type: operatorv1.ResourceEventType_UPDATED,
				},
			},
			expEvent: &Event[compapi.Component]{
				Manifest: compapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "myns"},
				},
				Type: operatorv1.ResourceEventType_CREATED,
			},
			expOK: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			td, err := spiffeid.TrustDomainFromString("example.org")
			require.NoError(t, err)
			id, err := spiffe.FromStrings(td, "myns", "myapp")
			require.NoError(t, err)

			h := &handler[compapi.Component]{
				id: id,
			}
			actual, ok := h.appEventFromEvent(test.event)
			assert.Equal(t, test.expOK, ok)
			assert.Equal(t, test.expEvent, actual)
		})
	}
}
