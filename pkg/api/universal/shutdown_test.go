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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcMetadata "google.golang.org/grpc/metadata"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestShutdown(t *testing.T) {
	type result struct {
		graceful bool
		exitCode int
	}

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
			resCh := make(chan result, 2)
			fakeAPI := New(Options{
				Logger: testLogger,
				ShutdownFn: func() {
					resCh <- result{graceful: true}
				},
				ExitFn: func(code int) {
					resCh <- result{exitCode: code}
				},
			})

			ctx := t.Context()
			if len(tc.metadata) > 0 {
				ctx = grpcMetadata.NewIncomingContext(ctx, grpcMetadata.New(tc.metadata))
			}

			_, err := fakeAPI.Shutdown(ctx, &runtimev1pb.ShutdownRequest{})
			require.NoError(t, err)

			select {
			case res := <-resCh:
				if tc.wantGrace {
					assert.True(t, res.graceful, "expected graceful shutdown, got %+v", res)
				}
				if tc.wantExit {
					assert.Equal(t, tc.wantCode, res.exitCode)
				}
			case <-time.After(time.Second):
				t.Fatal("no shutdown or exit invoked within 1 second")
			}
		})
	}
}

func TestShutdownNoForceWithoutMetadata(t *testing.T) {
	resCh := make(chan struct{}, 1)
	fakeAPI := New(Options{
		Logger: testLogger,
		ShutdownFn: func() {
			resCh <- struct{}{}
		},
		ExitFn: func(int) {
			t.Error("exitFn should not be called on graceful path")
		},
	})

	_, err := fakeAPI.Shutdown(context.Background(), &runtimev1pb.ShutdownRequest{})
	require.NoError(t, err)
	select {
	case <-resCh:
	case <-time.After(time.Second):
		t.Fatal("graceful shutdownFn not invoked within 1 second")
	}
}
