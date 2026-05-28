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

import "sync"

// Call tracks a single in-flight activity execution.
type Call struct {
	done chan struct{}
	once sync.Once
	err  error
}

func newCall() *Call {
	return &Call{done: make(chan struct{})}
}

// Done returns a channel that is closed when Finish has been called. After
// Done is closed, Err reports the outcome.
func (c *Call) Done() <-chan struct{} {
	return c.done
}

// Err returns the outcome of the call. Only valid after Done is closed. A nil
// return means the activity completed successfully and its result has been
// published to the workflow actor by the owner; followers should surface nil
// to their caller so the scheduler acks SUCCESS.
func (c *Call) Err() error {
	return c.err
}

// Finish closes the call with the given outcome. Idempotent. Only the owner
// should call this.
func (c *Call) Finish(err error) {
	c.once.Do(func() {
		c.err = err
		close(c.done)
	})
}
