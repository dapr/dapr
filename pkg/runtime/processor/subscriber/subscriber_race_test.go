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

package subscriber

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

func TestRun_concurrentWithStopAppSubscriptions(t *testing.T) {
	t.Parallel()

	for i := range 100 {
		subs := New(Options{
			CompStore:  compstore.New(),
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Namespace:  "ns1",
			AppID:      TestRuntimeConfigID,
			Channels:   new(channels.Channels).WithAppChannel(new(channelt.MockAppChannel)),
		})
		subs.appSubActive = true
		retryCtx, retryCancel := context.WithCancel(t.Context())
		t.Cleanup(retryCancel)
		subs.retryCtx["ps"] = retryCtx
		subs.retryCancel["ps"] = retryCancel

		runCtx, stopRun := context.WithCancel(t.Context())
		runErr := make(chan error, 1)
		go func() { runErr <- subs.Run(runCtx) }()

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Go(func() {
			<-start
			stopRun()
		})
		wg.Go(func() {
			<-start
			subs.StopAppSubscriptions()
		})
		close(start)
		wg.Wait()

		select {
		case err := <-runErr:
			require.NoError(t, err, "iteration %d", i)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "Run did not return", "iteration %d", i)
		}

		require.Error(t, retryCtx.Err(), "iteration %d", i)
		subs.lock.RLock()
		assert.Empty(t, subs.retryCtx, "iteration %d", i)
		assert.Empty(t, subs.retryCancel, "iteration %d", i)
		subs.lock.RUnlock()
		assert.True(t, subs.closed.Load(), "iteration %d", i)
	}
}
