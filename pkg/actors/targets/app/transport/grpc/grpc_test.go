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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/callbackstream"
	"github.com/dapr/dapr/pkg/actors/targets/app/transport"
	"github.com/dapr/dapr/pkg/config"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

func TestDeactivate_SendsMetadata(t *testing.T) {
	tests := map[string]struct {
		md   map[string]*internalv1pb.ListStringValue
		want map[string]string
	}{
		"activation id": {
			md:   map[string]*internalv1pb.ListStringValue{transport.ActivationIDHeader: {Values: []string{"act-1"}}},
			want: map[string]string{transport.ActivationIDHeader: "act-1"},
		},
		"no metadata": {},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			mgr := callbackstream.NewManager()
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = mgr.Run(ctx)
			}()
			t.Cleanup(func() {
				cancel()
				<-done
			})

			conn := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"myactortype"}})
			seen := make(chan *runtimev1pb.SubscribeActorEventsResponseDeactivateRequestAlpha1, 1)
			go func() {
				select {
				case <-conn.Done():
				case out := <-conn.Outbox:
					req := out.GetDeactivateRequest()
					seen <- req
					conn.Deliver(&runtimev1pb.SubscribeActorEventsRequestAlpha1{
						RequestType: &runtimev1pb.SubscribeActorEventsRequestAlpha1_DeactivateResponse{
							DeactivateResponse: &runtimev1pb.SubscribeActorEventsRequestDeactivateResponseAlpha1{Id: req.GetId()},
						},
					})
				}
			}()

			tp := New(mgr, resiliency.New(logger.NewLogger("test")), "myactortype")
			require.NoError(t, tp.Deactivate(t.Context(), "myactortype", "a", test.md))

			select {
			case req := <-seen:
				assert.Equal(t, "myactortype", req.GetActorType())
				assert.Equal(t, "a", req.GetActorId())
				assert.Equal(t, test.want, req.GetMetadata())
			case <-time.After(5 * time.Second):
				require.Fail(t, "deactivate request never sent")
			}
		})
	}
}
