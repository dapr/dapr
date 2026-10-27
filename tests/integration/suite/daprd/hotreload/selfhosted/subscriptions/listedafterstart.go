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

package subscriptions

import (
	"context"
	nethttp "net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(listedafterstart))
}

type listedafterstart struct {
	daprd  *daprd.Daprd
	sub    *subscriber.Subscriber
	logOut *log.Log
	resDir string

	subscribeCalled  chan struct{}
	subscribeRelease chan struct{}
	calledOnce       sync.Once
	releaseOnce      sync.Once
}

func (l *listedafterstart) Setup(t *testing.T) []framework.Option {
	l.subscribeCalled = make(chan struct{})
	l.subscribeRelease = make(chan struct{})

	l.sub = subscriber.New(t,
		subscriber.WithRoutes("/a"),
		subscriber.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			l.calledOnce.Do(func() { close(l.subscribeCalled) })
			select {
			case <-l.subscribeRelease:
			case <-r.Context().Done():
			}
			w.Write([]byte("[]"))
		}),
	)

	l.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(l.resDir, "pubsub.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'pubsub0'
spec:
 type: pubsub.in-memory
 version: v1
`), 0o600))

	l.logOut = log.New()
	l.daprd = daprd.New(t,
		daprd.WithExecOptions(exec.WithStdout(l.logOut), exec.WithStderr(l.logOut)),
		daprd.WithAppPort(l.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourcesDir(l.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(l.sub, l.daprd),
	}
}

func (l *listedafterstart) Run(t *testing.T, ctx context.Context) {
	release := func() { l.releaseOnce.Do(func() { close(l.subscribeRelease) }) }
	t.Cleanup(release)

	select {
	case <-l.subscribeCalled:
	case <-time.After(time.Second * 10):
		require.Fail(t, "daprd never asked the app for its subscriptions")
	}
	// daprd reports healthy only once app subscriptions started, but its
	// API and the hot reloader already run.
	l.daprd.WaitUntilTCPReady(t, ctx)
	require.Empty(t, l.daprd.GetMetaSubscriptions(t, ctx))

	require.NoError(t, os.WriteFile(filepath.Join(l.resDir, "sub.yaml"), []byte(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: 'sub1'
spec:
 pubsubname: 'pubsub0'
 topic: 'a'
 routes:
  default: '/a'
`), 0o600))

	// The hot reloader handed the subscription over; its start now waits for
	// app subscriptions to finish starting.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, l.logOut.Contains("Adding Subscription for processing: sub1"))
	}, time.Second*10, time.Millisecond*10)
	for deadline := time.Now().Add(time.Second * 2); time.Now().Before(deadline); time.Sleep(time.Millisecond * 50) {
		require.Empty(t, l.daprd.GetMetaSubscriptions(t, ctx),
			"a subscription must not be listed before its consumer starts")
	}

	release()
	l.daprd.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, l.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)
	l.sub.ExpectPublishReceive(t, ctx, subscriber.PublishRequest{
		Daprd:      l.daprd,
		PubSubName: "pubsub0",
		Topic:      "a",
		Data:       `{"status": "completed"}`,
	})
}
