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

package concurrency

import (
	"context"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(concurrent))
}

// concurrent verifies end-to-end that an in-memory actor timer callback for one
// actor does not block the timer callback of a *different* actor on the same
// sidecar: callbacks for different actors run concurrently. With a single serial
// timer executor (the pre-fix behavior) this test fails — the "fast" actor's
// timer never fires while the "slow" actor's callback is held.
type concurrent struct {
	app *actors.Actors

	slowStarted chan struct{}
	releaseSlow chan struct{}
	fastFired   chan struct{}
	slowOnce    sync.Once
	fastOnce    sync.Once
}

func (c *concurrent) Setup(t *testing.T) []framework.Option {
	c.slowStarted = make(chan struct{})
	c.releaseSlow = make(chan struct{})
	c.fastFired = make(chan struct{})

	c.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				return // actor deactivation
			}
			switch {
			case strings.Contains(r.URL.Path, "/abc/slow/"):
				c.slowOnce.Do(func() { close(c.slowStarted) })
				<-c.releaseSlow // hold this callback "in flight"
			case strings.Contains(r.URL.Path, "/abc/fast/"):
				c.fastOnce.Do(func() { close(c.fastFired) })
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(c.app),
	}
}

func (c *concurrent) Run(t *testing.T, ctx context.Context) {
	c.app.WaitUntilRunning(t, ctx)

	// Ensure the held callback is always released so daprd/app shut down cleanly,
	// regardless of which assertion path the test takes.
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(c.releaseSlow) }) }
	t.Cleanup(release)

	client := c.app.GRPCClient(t, ctx)

	// Register a timer on the "slow" actor and wait until its callback is in
	// flight (the handler blocks until released).
	_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "slow",
		Name:      "tick",
		DueTime:   "0s",
		Period:    "10s",
	})
	require.NoError(t, err)

	select {
	case <-c.slowStarted:
	case <-time.After(15 * time.Second):
		t.Fatal("slow actor timer never fired")
	}

	// With the slow actor's callback occupying the timer executor, register a
	// timer on a DIFFERENT actor. It must fire even though the slow callback is
	// still blocked — timer callbacks for different actors run concurrently. A
	// single serial executor would block this indefinitely.
	_, err = client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "fast",
		Name:      "tick",
		DueTime:   "0s",
		Period:    "10s",
	})
	require.NoError(t, err)

	select {
	case <-c.fastFired:
	case <-time.After(10 * time.Second):
		t.Fatal("fast actor timer was blocked by the slow actor's in-flight timer " +
			"callback: in-memory timer callbacks are not running concurrently across actors")
	}
}
