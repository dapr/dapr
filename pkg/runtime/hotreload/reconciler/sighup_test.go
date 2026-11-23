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

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/healthz"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/fake"
)

func Test_SIGHUPReconciler(t *testing.T) {
	endpoint := func(name, baseURL string) httpendpointapi.HTTPEndpoint {
		return httpendpointapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: httpendpointapi.HTTPEndpointSpec{
				BaseURL: baseURL,
				ClientTLS: &common.TLS{RootCA: &common.TLSDocument{
					SecretKeyRef: &common.SecretKeyRef{Name: "certs", Key: "ca"},
				}},
			},
		}
	}

	// The processor stores endpoints with their secrets resolved, so the
	// component store copy never equals the one served by the loader.
	resolved := func(e httpendpointapi.HTTPEndpoint) httpendpointapi.HTTPEndpoint {
		e = *e.DeepCopy()
		e.Spec.ClientTLS.RootCA.Value = &common.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(`"ca-pem"`)}}
		return e
	}

	event := func(typ operatorv1pb.ResourceEventType, e httpendpointapi.HTTPEndpoint) *loader.Event[httpendpointapi.HTTPEndpoint] {
		return &loader.Event[httpendpointapi.HTTPEndpoint]{Type: typ, Resource: e}
	}

	fooA := endpoint("foo", "https://a")
	fooB := endpoint("foo", "https://b")

	tests := map[string]struct {
		stored     []httpendpointapi.HTTPEndpoint
		events     []*loader.Event[httpendpointapi.HTTPEndpoint]
		expSignals int
	}{
		"UPDATED equal to the stored resource once resolved does not signal": {
			stored: []httpendpointapi.HTTPEndpoint{resolved(fooA)},
			events: []*loader.Event[httpendpointapi.HTTPEndpoint]{
				event(operatorv1pb.ResourceEventType_UPDATED, fooA),
				event(operatorv1pb.ResourceEventType_UPDATED, fooA),
			},
			expSignals: 0,
		},
		"UPDATED with a changed spec signals": {
			stored: []httpendpointapi.HTTPEndpoint{resolved(fooA)},
			events: []*loader.Event[httpendpointapi.HTTPEndpoint]{
				event(operatorv1pb.ResourceEventType_UPDATED, fooB),
			},
			expSignals: 1,
		},
		"CREATED for a resource not in the store signals": {
			events: []*loader.Event[httpendpointapi.HTTPEndpoint]{
				event(operatorv1pb.ResourceEventType_CREATED, fooA),
			},
			expSignals: 1,
		},
		"DELETED of a stored resource signals": {
			stored: []httpendpointapi.HTTPEndpoint{resolved(fooA)},
			events: []*loader.Event[httpendpointapi.HTTPEndpoint]{
				event(operatorv1pb.ResourceEventType_DELETED, fooA),
			},
			expSignals: 1,
		},
		"DELETED of a resource not in the store does not signal": {
			events: []*loader.Event[httpendpointapi.HTTPEndpoint]{
				event(operatorv1pb.ResourceEventType_DELETED, fooA),
			},
			expSignals: 0,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			store := compstore.New()
			for _, e := range test.stored {
				store.AddHTTPEndpoint(e)
			}

			r := NewSIGHUPHTTPEndpoints(Options[httpendpointapi.HTTPEndpoint]{
				Loader:    fake.New(),
				CompStore: store,
				Healthz:   healthz.New(),
			})
			r.resolve = func(_ context.Context, e httpendpointapi.HTTPEndpoint) httpendpointapi.HTTPEndpoint {
				return resolved(e)
			}

			eventCh := make(chan *loader.Event[httpendpointapi.HTTPEndpoint])
			r.loader = fake.NewFake[httpendpointapi.HTTPEndpoint]().
				WithStream(func(context.Context) (*loader.StreamConn[httpendpointapi.HTTPEndpoint], error) {
					return &loader.StreamConn[httpendpointapi.HTTPEndpoint]{
						EventCh:     eventCh,
						ReconcileCh: make(chan struct{}),
					}, nil
				})

			var signals int
			r.signal = func() error {
				signals++
				return nil
			}

			ctx, cancel := context.WithCancel(t.Context())
			errCh := make(chan error, 1)
			go func() { errCh <- r.Run(ctx) }()

			for _, e := range test.events {
				select {
				case eventCh <- e:
				case <-time.After(time.Second * 3):
					require.FailNow(t, "event not consumed in time")
				}
			}

			// The loop drains every queued event before Run returns.
			cancel()
			select {
			case err := <-errCh:
				require.NoError(t, err)
			case <-time.After(time.Second * 3):
				require.FailNow(t, "reconciler did not return in time")
			}

			assert.Equal(t, test.expSignals, signals)
		})
	}
}
