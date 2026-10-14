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

package inflight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/kit/ptr"
)

func acquireClaim(t *testing.T, i *Inflight, ctx context.Context, actorType string) *loops.LockResponse {
	t.Helper()

	respCh := make(chan *loops.LockResponse, 1)
	i.Acquire(&loops.LockRequest{
		ActorType: actorType,
		Context:   ctx,
		Response:  respCh,
	})
	select {
	case resp := <-respCh:
		require.NotNil(t, resp)
		return resp
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Acquire did not resolve")
		return nil
	}
}

func requireClaimDone(t *testing.T, resp *loops.LockResponse, msgAndArgs ...any) {
	t.Helper()

	select {
	case <-resp.Context.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "claim was not cancelled", msgAndArgs...)
	}
}

func TestClose_ForceCancelRacesCallerCancel(t *testing.T) {
	t.Parallel()

	for n := range 300 {
		ctx, cancel := context.WithCancel(t.Context())

		i := New(Options{Hostname: "h", Port: "1"})
		i.Set(newTables(100, map[string]map[string]int64{"a": {"h:1": 1}}), 1)
		i.SetDrainOngoingCallTimeout(nil, ptr.Of(time.Nanosecond))
		i.Open(ctx)

		claim := acquireClaim(t, i, ctx, "a")

		done := make(chan struct{})
		go func() {
			defer close(done)
			claim.Cancel(nil)
		}()

		i.Close(errors.New("placement stream closed"))
		<-done

		requireClaimDone(t, claim, "iteration %d", n)
		cancel()
	}
}

func TestClose_StaleClaimDoesNotReleaseNextSessionClaim(t *testing.T) {
	t.Parallel()

	for n := range 300 {
		ctx, cancel := context.WithCancel(t.Context())

		i := New(Options{Hostname: "h", Port: "1"})
		i.Set(newTables(100, map[string]map[string]int64{"a": {"h:1": 1}}), 1)
		i.SetDrainOngoingCallTimeout(nil, ptr.Of(10*time.Millisecond))

		i.Open(ctx)
		reqCtx, endReq := context.WithCancel(ctx)
		stale := acquireClaim(t, i, reqCtx, "a")
		endReq()
		i.Close(errors.New("first session closed"))

		staleDone := make(chan struct{})
		go func() {
			defer close(staleDone)
			stale.Cancel(nil)
		}()

		i.Open(ctx)
		live := acquireClaim(t, i, ctx, "a")
		<-staleDone

		closeErr := errors.New("second session closed")
		i.Close(closeErr)
		requireClaimDone(t, live, "iteration %d", n)
		require.ErrorIs(t, context.Cause(live.Context), closeErr, "iteration %d", n)

		cancel()
	}
}
