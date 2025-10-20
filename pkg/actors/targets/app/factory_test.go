/*
Copyright 2025 The Dapr Authors
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

package app

import (
	"context"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/channel/fake"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func Test_GetOrCreate(t *testing.T) {
	fact := New(Options{
		Reentrancy:  reentrancystore.New(),
		IdleTimeout: time.Second * 10,
		AppChannel: fake.New().WithInvokeMethod(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	ff := fact.(*factory)
	assert.Equal(t, 0, mapLen(ff))

	fact.GetOrCreate("foo1")
	fact.GetOrCreate("foo2")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo3")

	assert.Equal(t, 3, mapLen(ff))
	assert.True(t, fact.Exists("foo1"))
	assert.True(t, fact.Exists("foo2"))
	assert.True(t, fact.Exists("foo3"))
	assert.False(t, fact.Exists("foo4"))

	act := fact.GetOrCreate("foo3")
	require.NoError(t, act.Deactivate(t.Context()))
	assert.Equal(t, 2, mapLen(ff))
	assert.False(t, fact.Exists("foo3"))
	fact.GetOrCreate("foo3")
	assert.Equal(t, 3, mapLen(ff))
	assert.True(t, fact.Exists("foo2"))

	act = fact.GetOrCreate("foo1")
	require.NoError(t, act.Deactivate(t.Context()))
	act = fact.GetOrCreate("foo2")
	require.NoError(t, act.Deactivate(t.Context()))
	act = fact.GetOrCreate("foo3")
	require.NoError(t, act.Deactivate(t.Context()))
	assert.Equal(t, 0, mapLen(ff))
}

func Test_HaltAll(t *testing.T) {
	fact := New(Options{
		Reentrancy:  reentrancystore.New(),
		IdleTimeout: time.Second * 10,
		AppChannel: fake.New().WithInvokeMethod(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	ff := fact.(*factory)
	assert.Equal(t, 0, mapLen(ff))

	fact.GetOrCreate("foo1")
	fact.GetOrCreate("foo2")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo4")
	fact.GetOrCreate("foo5")

	assert.Equal(t, 5, mapLen(ff))

	require.NoError(t, fact.HaltAll(t.Context()))
	assert.Equal(t, 0, mapLen(ff))
}

func Test_HaltNonHosted(t *testing.T) {
	hosted := map[string]bool{
		"foo1": true,
		"foo2": true,
		"foo5": true,
		"foo7": true,
	}

	pl := placementfake.New().WithIsActorHosted(func(_ context.Context, _, actorID string) bool {
		return hosted[actorID]
	})

	fact := New(Options{
		Reentrancy:  reentrancystore.New(),
		Placement:   pl,
		IdleTimeout: time.Second * 10,
		AppChannel: fake.New().WithInvokeMethod(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	ff := fact.(*factory)
	assert.Equal(t, 0, mapLen(ff))

	fact.GetOrCreate("foo1")
	fact.GetOrCreate("foo2")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo4")
	fact.GetOrCreate("foo5")
	fact.GetOrCreate("foo6")
	fact.GetOrCreate("foo7")

	assert.Equal(t, 7, mapLen(ff))

	require.NoError(t, fact.HaltNonHosted(t.Context()))
	assert.Equal(t, 4, mapLen(ff))
}

func Test_Idle(t *testing.T) {
	clock := clocktesting.NewFakeClock(time.Now())
	fact := New(Options{
		Reentrancy:  reentrancystore.New(),
		clock:       clock,
		Placement:   placementfake.New(),
		IdleTimeout: time.Second * 10,
		AppChannel: fake.New().WithInvokeMethod(func(_ context.Context, _ *invokev1.InvokeMethodRequest, ss string) (*invokev1.InvokeMethodResponse, error) {
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	ff := fact.(*factory)
	assert.Equal(t, 0, mapLen(ff))

	fact.GetOrCreate("foo1")
	fact.GetOrCreate("foo2")
	fact.GetOrCreate("foo3")
	fact.GetOrCreate("foo4")
	fact.GetOrCreate("foo5")
	fact.GetOrCreate("foo6")
	fact.GetOrCreate("foo7")
	assert.Equal(t, 7, mapLen(ff))

	assert.Eventually(t, clock.HasWaiters, time.Second*10, time.Millisecond*10)

	clock.Step(time.Second * 5)
	assert.True(t, clock.HasWaiters())
	assert.Equal(t, 7, mapLen(ff))
	clock.Step(time.Second * 5)
	assert.Eventually(t, func() bool { return !clock.HasWaiters() }, time.Second*10, time.Millisecond*10)
	assert.Equal(t, 0, mapLen(ff))
}

func mapLen(ff *factory) int {
	var i int
	ff.table.Range(func(any, any) bool {
		i++
		return true
	})
	return i
}

func Test_DeleteCacheRace(t *testing.T) {
	fact := New(Options{
		Reentrancy:  reentrancystore.New(),
		IdleTimeout: time.Millisecond * 10,
		Placement:   placementfake.New(),
		AppChannel: fake.New().WithInvokeMethod(func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error) {
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})

	const n = 10000
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			target := fact.GetOrCreate(strconv.Itoa(i))
			//nolint:gosec
			time.Sleep(time.Duration(rand.Intn(5)) * time.Millisecond)
			target.InvokeMethod(t.Context(), &internalv1pb.InternalInvokeRequest{})
			wg.Done()
		}(i)
	}
	wg.Wait()
}
