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

package drain

import (
	"context"
	nethttp "net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(failedlookup))
}

// failedlookup tests that failed actor lookups do not hold up the next
// dissemination round for that actor type. A caller invokes an actor type
// that no sidecar hosts yet, so every lookup fails while the router retries.
// When a sidecar that hosts the type joins, the round for the type drains the
// in-flight claims of the type on the caller. The failed lookups hold no
// claims, so the round completes without waiting for the drain timeout of the
// caller, which is longer than the dissemination timeout of placement.
type failedlookup struct {
	caller *actors.Actors
	host   *actors.Actors
}

func (f *failedlookup) Setup(t *testing.T) []framework.Option {
	f.caller = actors.New(t,
		actors.WithActorTypes("callertype"),
		actors.WithDrainOngoingCallTimeout(time.Second*20),
	)

	f.host = actors.New(t,
		actors.WithPeerActor(f.caller),
		actors.WithActorTypes("hostedtype"),
		actors.WithActorTypeHandler("hostedtype", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(f.caller),
	}
}

func (f *failedlookup) Run(t *testing.T, ctx context.Context) {
	f.caller.WaitUntilRunning(t, ctx)

	gclient := f.caller.GRPCClient(t, ctx)

	invokeCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	var succeeded atomic.Bool
	failed := make(chan struct{})
	wg.Go(func() {
		var once sync.Once
		for invokeCtx.Err() == nil {
			_, err := gclient.InvokeActor(invokeCtx, &rtv1.InvokeActorRequest{
				ActorType: "hostedtype",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			if err == nil {
				succeeded.Store(true)
				return
			}
			once.Do(func() { close(failed) })
		}
	})

	// The first invocation returns after the router used all its lookup
	// retries. The next invocation is then retrying when the host joins.
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		assert.Fail(t, "invocation of an actor type with no host should fail")
		return
	}

	f.host.Run(t, ctx)
	t.Cleanup(func() { f.host.Cleanup(t) })
	f.host.WaitUntilRunning(t, ctx)

	// The window covers host registration, the dissemination round and the
	// router's 1s retry backoff. Without the fix, the round waits for the
	// caller's 20s drain timeout (WithDrainOngoingCallTimeout in Setup), so
	// the window must stay well below that.
	assert.Eventually(t, succeeded.Load, time.Second*10, time.Millisecond*10)
}
