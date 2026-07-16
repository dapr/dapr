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
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemory "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

// grpc verifies resiliency retry `matching` on the publish path. "pubsub-match" retries
// only gRPCStatusCodes "14". "pubsub-nomatch" has no matching and retries everything — so
// the same FailedPrecondition error is the control: dropped under matching, retried without.
type grpc struct {
	daprd    *daprd.Daprd
	counters sync.Map
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	publishFn := func(ctx context.Context, req *contribpubsub.PublishRequest) error {
		c, _ := g.counters.LoadOrStore(req.Topic, &atomic.Int32{})
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

	g.daprd = daprd.New(t,
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
		framework.WithProcesses(comp, g.daprd),
	}
}

func (g *grpc) count(topic string) int {
	c, ok := g.counters.Load(topic)
	if !ok {
		return 0
	}
	return int(c.(*atomic.Int32).Load())
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)

	t.Run("matching: retriable code is retried up to maxRetries", func(t *testing.T) {
		_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "pubsub-match", Topic: "retriable",
			Data: []byte(`{"id":1}`),
		})
		require.Error(t, err)
		assert.Equal(t, 4, g.count("retriable"))
	})

	t.Run("matching: non-retriable code is not retried", func(t *testing.T) {
		_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "pubsub-match", Topic: "terminal",
			Data: []byte(`{"id":1}`),
		})
		require.Error(t, err)
		assert.Equal(t, 1, g.count("terminal"))
	})

	t.Run("no matching: every error is retried regardless of code", func(t *testing.T) {
		_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "pubsub-nomatch", Topic: "terminalnomatcherrcode",
			Data: []byte(`{"id":1}`),
		})
		require.Error(t, err)
		assert.Equal(t, 4, g.count("terminalnomatcherrcode"))
	})
}
