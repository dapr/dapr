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

package publish

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemory "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

// http is the HTTP-API equivalent of grpc: the same `matching` checks, but publishing via
// the /v1.0/publish HTTP endpoint instead of the gRPC API.
type http struct {
	daprd    *daprd.Daprd
	counters sync.Map
}

func (h *http) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	publishFn := func(ctx context.Context, req *contribpubsub.PublishRequest) error {
		c, _ := h.counters.LoadOrStore(req.Topic, &atomic.Int32{})
		c.(*atomic.Int32).Add(1)
		switch {
		case req.Topic == "retriable":
			return status.Error(codes.Unavailable, "broker unavailable")
		case strings.HasPrefix(req.Topic, "terminal"):
			return status.Error(codes.FailedPrecondition, "invalid topic")
		default:
			return nil
		}
	}

	sock := socket.New(t)
	comp := pubsub.New(t,
		pubsub.WithSocket(sock),
		pubsub.WithPubSub(inmemory.NewWrappedInMemory(t,
			inmemory.WithFeatures(),
			inmemory.WithPublishFn(publishFn),
		)),
	)

	h.daprd = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    retries:
      matchRetry:
        policy: constant
        duration: 10ms
        maxRetries: 3
        matching:
          gRPCStatusCodes: "14"
      noMatchRetry:
        policy: constant
        duration: 10ms
        maxRetries: 3
  targets:
    components:
      pubsub-match:
        outbound:
          retry: matchRetry
      pubsub-nomatch:
        outbound:
          retry: noMatchRetry
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pubsub-match
spec:
  type: pubsub.%[1]s
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pubsub-nomatch
spec:
  type: pubsub.%[1]s
  version: v1
`, comp.SocketName())),
		daprd.WithSocket(t, sock),
	)

	return []framework.Option{
		framework.WithProcesses(comp, h.daprd),
	}
}

func (h *http) count(topic string) int {
	c, ok := h.counters.Load(topic)
	if !ok {
		return 0
	}
	return int(c.(*atomic.Int32).Load())
}

func (h *http) publish(t *testing.T, ctx context.Context, httpClient *stdhttp.Client, pubsubName, topic string) {
	t.Helper()
	reqURL := fmt.Sprintf("http://%s/v1.0/publish/%s/%s", h.daprd.HTTPAddress(), pubsubName, topic)
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, reqURL, strings.NewReader(`{"id":1}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.NotEqual(t, stdhttp.StatusNoContent, resp.StatusCode, reqURL)
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	t.Run("matching: retriable code is retried up to maxRetries", func(t *testing.T) {
		h.publish(t, ctx, httpClient, "pubsub-match", "retriable")
		assert.Equal(t, 4, h.count("retriable"))
	})

	t.Run("matching: non-retriable code is not retried", func(t *testing.T) {
		h.publish(t, ctx, httpClient, "pubsub-match", "terminal")
		assert.Equal(t, 1, h.count("terminal"))
	})

	t.Run("no matching: every error is retried regardless of code", func(t *testing.T) {
		h.publish(t, ctx, httpClient, "pubsub-nomatch", "terminalnomatcherrcode")
		assert.Equal(t, 4, h.count("terminalnomatcherrcode"))
	})
}
