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

package universal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcMetadata "google.golang.org/grpc/metadata"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestShutdown(t *testing.T) {
	const negativeWait = 200 * time.Millisecond

	tests := map[string]struct {
		metadata  map[string]string
		wantGrace bool
		wantExit  bool
		wantCode  int
	}{
		"no metadata triggers graceful shutdown": {
			wantGrace: true,
		},
		"dapr-force-shutdown=true triggers force exit": {
			metadata: map[string]string{ForceShutdownMetadataKey: "true"},
			wantExit: true,
			wantCode: 1,
		},
		"dapr-force-shutdown=TRUE is case-insensitive": {
			metadata: map[string]string{ForceShutdownMetadataKey: "TRUE"},
			wantExit: true,
			wantCode: 1,
		},
		"dapr-force-shutdown=false falls back to graceful": {
			metadata:  map[string]string{ForceShutdownMetadataKey: "false"},
			wantGrace: true,
		},
		"unrelated metadata falls back to graceful": {
			metadata:  map[string]string{"x-some-header": "true"},
			wantGrace: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			shutdownCh := make(chan struct{}, 1)
			exitCh := make(chan int, 1)
			fakeAPI := New(Options{
				Logger: testLogger,
				ShutdownFn: func() {
					shutdownCh <- struct{}{}
				},
				ExitFn: func(code int) {
					exitCh <- code
				},
			})

			ctx := t.Context()
			if len(tc.metadata) > 0 {
				ctx = grpcMetadata.NewIncomingContext(ctx, grpcMetadata.New(tc.metadata))
			}

			_, err := fakeAPI.Shutdown(ctx, &runtimev1pb.ShutdownRequest{})
			require.NoError(t, err)

			if tc.wantGrace {
				select {
				case <-shutdownCh:
				case <-time.After(time.Second):
					t.Fatal("graceful shutdownFn not invoked within 1 second")
				}
				// Ensure exitFn is never invoked on the graceful path.
				select {
				case code := <-exitCh:
					t.Fatalf("exitFn unexpectedly invoked with code %d on graceful path", code)
				case <-time.After(negativeWait):
				}
			}
			if tc.wantExit {
				select {
				case code := <-exitCh:
					assert.Equal(t, tc.wantCode, code)
				case <-time.After(time.Second):
					t.Fatal("exitFn not invoked within 1 second")
				}
				// Ensure shutdownFn is never invoked on the force path.
				select {
				case <-shutdownCh:
					t.Fatal("graceful shutdownFn unexpectedly invoked on force path")
				case <-time.After(negativeWait):
				}
			}
		})
	}
}
