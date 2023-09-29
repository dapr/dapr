/*
Copyright 2023 The Dapr Authors
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

package util

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ParallelTest is a helper for running tests in parallel without having to
// create new Go test functions.
type ParallelTest struct {
	lock sync.Mutex
	fns  []func(*assert.CollectT)
}

// NewParallel creates a new ParallelTest.
// Tests are executed during given test's cleanup.
func NewParallel(t *testing.T, fns ...func(*assert.CollectT)) *ParallelTest {
	p := &ParallelTest{
		fns: fns,
	}
	t.Cleanup(func() {
		p.lock.Lock()
		defer p.lock.Unlock()
		t.Helper()

		ch := make(chan any)
		collects := make([]*assert.CollectT, len(p.fns))
		for i, fn := range p.fns {
			collects[i] = new(assert.CollectT)
			go func(i int, fn func(*assert.CollectT)) {
				defer func() {
					if r := recover(); r != nil {
						ch <- r
					}
				}()

				fn(collects[i])
				ch <- nil
			}(i, fn)
		}

		for i := 0; i < len(p.fns); i++ {
			assert.Nil(t, <-ch)
		}

		for _, collect := range collects {
			collect.Copy(t)
		}
	})

	return p
}

// Add adds a test function to be executed in parallel.
func (p *ParallelTest) Add(fn func(*assert.CollectT)) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.fns = append(p.fns, fn)
}
