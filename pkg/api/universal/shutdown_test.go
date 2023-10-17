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

	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestShutdownEndpoint(t *testing.T) {
	shutdownCh := make(chan struct{})

	fakeAPI := &Universal{
		logger: testLogger,
		shutdownFn: func() {
			close(shutdownCh)
		},
	}

	t.Run("Shutdown successfully", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		_, err := fakeAPI.Shutdown(ctx, &runtimev1pb.ShutdownRequest{})
		cancel()
		require.NoError(t, err, "Expected no error")
		select {
		case <-time.After(time.Second):
			t.Fatal("Did not shut down within 1 second")
		case <-shutdownCh:
			// All good
		}
	})
}
