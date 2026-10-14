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

package pending

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_deliverNoWaiter(t *testing.T) {
	t.Parallel()

	p := New()
	assert.False(t, p.Deliver("a/1", []byte("result")))
	assert.False(t, p.Cancel("a/1"))
}

func Test_registerReplacesAndCancelsPrevious(t *testing.T) {
	t.Parallel()

	p := New()
	var first, second *Result
	dereg1 := p.RegisterCallback("a/1", func(res Result) { first = &res })
	dereg2 := p.RegisterCallback("a/1", func(res Result) { second = &res })
	t.Cleanup(dereg1)
	t.Cleanup(dereg2)

	require.NotNil(t, first)
	assert.True(t, first.Cancelled, "superseded waiter must be cancelled")

	require.True(t, p.Deliver("a/1", []byte("result")))
	require.NotNil(t, second)
	assert.Equal(t, []byte("result"), second.Data)
}

func Test_staleDeregisterDoesNotRemoveNewWaiter(t *testing.T) {
	t.Parallel()

	p := New()
	dereg1 := p.RegisterCallback("a/1", func(Result) {})
	dereg2 := p.RegisterCallback("a/1", func(Result) {})
	t.Cleanup(dereg2)

	dereg1()
	assert.Equal(t, 1, p.Len(), "stale deregister must not remove the new waiter")
	assert.True(t, p.Deliver("a/1", []byte("result")))
}

func Test_registerCallbackDeliver(t *testing.T) {
	t.Parallel()

	p := New()
	var got *Result
	dereg := p.RegisterCallback("a/1", func(res Result) {
		got = &res
	})
	t.Cleanup(dereg)

	require.True(t, p.Deliver("a/1", []byte("result")))
	require.NotNil(t, got, "callback must run before Deliver returns")
	assert.Equal(t, []byte("result"), got.Data)
	assert.False(t, got.Cancelled)
	assert.Equal(t, 1, p.Len(), "a callback registration must stay armed across deliveries")

	// The consumer can discard a delivery as stale (completion-token guard)
	// and keep waiting: a later delivery must reach the same callback.
	require.True(t, p.Deliver("a/1", []byte("second")))
	assert.Equal(t, []byte("second"), got.Data)

	dereg()
	assert.Equal(t, 0, p.Len())
	assert.False(t, p.Deliver("a/1", []byte("third")), "a deregistered callback must not fire")
	assert.Equal(t, []byte("second"), got.Data)
}

func Test_registerCallbackCancel(t *testing.T) {
	t.Parallel()

	p := New()
	var got *Result
	dereg := p.RegisterCallback("a/1", func(res Result) {
		got = &res
	})
	t.Cleanup(dereg)

	require.True(t, p.Cancel("a/1"))
	require.NotNil(t, got)
	assert.True(t, got.Cancelled)
	assert.Equal(t, 1, p.Len(), "cancellation delivers but only deregister removes")
}

func Test_callbackDeliverDeferred(t *testing.T) {
	t.Parallel()

	p := New()
	var got *Result
	dereg := p.RegisterCallback("a/1", func(res Result) {
		got = &res
	})
	t.Cleanup(dereg)

	run, ok := p.DeliverDeferred("a/1", []byte("result"))
	require.True(t, ok)
	require.NotNil(t, run, "callback waiter delivery must be returned as a thunk")
	assert.Nil(t, got, "callback must not run before the thunk")
	assert.Equal(t, 1, p.Len(), "the registration stays armed for later deliveries")

	run()
	require.NotNil(t, got)
	assert.Equal(t, []byte("result"), got.Data)
}

func Test_registerCallbackReplacesAndCancelsPrevious(t *testing.T) {
	t.Parallel()

	p := New()
	var got *Result
	_ = p.RegisterCallback("a/1", func(res Result) {
		got = &res
	})
	var second *Result
	dereg := p.RegisterCallback("a/1", func(res Result) { second = &res })
	t.Cleanup(dereg)

	require.NotNil(t, got, "superseded callback waiter must be cancelled on the registering goroutine")
	assert.True(t, got.Cancelled)

	require.True(t, p.Deliver("a/1", []byte("result")))
	require.NotNil(t, second)
	assert.Equal(t, []byte("result"), second.Data)
}

func Test_callbackDeregisterPreventsDelivery(t *testing.T) {
	t.Parallel()

	p := New()
	fired := false
	dereg := p.RegisterCallback("a/1", func(Result) {
		fired = true
	})
	dereg()

	assert.False(t, p.Deliver("a/1", []byte("result")))
	assert.False(t, fired)
	assert.Equal(t, 0, p.Len())
}

func Test_callbackReentrantRegister(t *testing.T) {
	t.Parallel()

	p := New()
	var inner *Result
	dereg := p.RegisterCallback("a/1", func(Result) {
		// The delivering goroutine re-enters the registry, as a work item
		// continuation dispatching the next turn does.
		p.RegisterCallback("a/2", func(res Result) {
			inner = &res
		})
	})
	t.Cleanup(dereg)

	require.True(t, p.Deliver("a/1", []byte("x")))
	require.True(t, p.Deliver("a/2", []byte("y")))
	require.NotNil(t, inner)
	assert.Equal(t, []byte("y"), inner.Data)
}

func Test_concurrent(t *testing.T) {
	t.Parallel()

	p := New()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(2)
		key := string(rune('a'+i%26)) + "/" + string(rune('0'+i%10))
		delivered := make(chan struct{}, 1)
		dereg := p.RegisterCallback(key, func(Result) {
			select {
			case delivered <- struct{}{}:
			default:
			}
		})
		go func() {
			defer wg.Done()
			p.Deliver(key, []byte("x"))
		}()
		go func() {
			defer wg.Done()
			defer dereg()
			<-delivered
		}()
	}
	wg.Wait()
}
