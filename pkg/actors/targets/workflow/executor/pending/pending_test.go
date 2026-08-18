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

func Test_registerDeliver(t *testing.T) {
	t.Parallel()

	p := New()
	ch, dereg := p.Register("a/1")
	t.Cleanup(dereg)

	require.True(t, p.Deliver("a/1", []byte("result")))

	res := <-ch
	assert.Equal(t, []byte("result"), res.Data)
	assert.False(t, res.Cancelled)
	assert.Equal(t, 0, p.Len())
}

func Test_registerCancel(t *testing.T) {
	t.Parallel()

	p := New()
	ch, dereg := p.Register("a/1")
	t.Cleanup(dereg)

	require.True(t, p.Cancel("a/1"))

	res := <-ch
	assert.True(t, res.Cancelled)
	assert.Equal(t, 0, p.Len())
}

func Test_deliverNoWaiter(t *testing.T) {
	t.Parallel()

	p := New()
	assert.False(t, p.Deliver("a/1", []byte("result")))
	assert.False(t, p.Cancel("a/1"))
}

func Test_deliverAfterDeregister(t *testing.T) {
	t.Parallel()

	p := New()
	_, dereg := p.Register("a/1")
	dereg()

	assert.False(t, p.Deliver("a/1", []byte("result")))
	assert.Equal(t, 0, p.Len())
}

func Test_duplicateDeliver(t *testing.T) {
	t.Parallel()

	p := New()
	ch, dereg := p.Register("a/1")
	t.Cleanup(dereg)

	require.True(t, p.Deliver("a/1", []byte("first")))
	assert.False(t, p.Deliver("a/1", []byte("second")))

	res := <-ch
	assert.Equal(t, []byte("first"), res.Data)
}

func Test_registerReplacesAndCancelsPrevious(t *testing.T) {
	t.Parallel()

	p := New()
	ch1, dereg1 := p.Register("a/1")
	ch2, dereg2 := p.Register("a/1")
	t.Cleanup(dereg1)
	t.Cleanup(dereg2)

	res := <-ch1
	assert.True(t, res.Cancelled, "superseded waiter must be cancelled")

	require.True(t, p.Deliver("a/1", []byte("result")))
	res = <-ch2
	assert.Equal(t, []byte("result"), res.Data)
}

func Test_staleDeregisterDoesNotRemoveNewWaiter(t *testing.T) {
	t.Parallel()

	p := New()
	ch1, dereg1 := p.Register("a/1")
	_, dereg2 := p.Register("a/1")
	t.Cleanup(dereg2)
	<-ch1

	dereg1()
	assert.Equal(t, 1, p.Len(), "stale deregister must not remove the new waiter")
	assert.True(t, p.Deliver("a/1", []byte("result")))
}

func Test_concurrent(t *testing.T) {
	t.Parallel()

	p := New()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(2)
		key := string(rune('a'+i%26)) + "/" + string(rune('0'+i%10))
		ch, dereg := p.Register(key)
		go func() {
			defer wg.Done()
			p.Deliver(key, []byte("x"))
		}()
		go func() {
			defer wg.Done()
			defer dereg()
			<-ch
		}()
	}
	wg.Wait()
}
