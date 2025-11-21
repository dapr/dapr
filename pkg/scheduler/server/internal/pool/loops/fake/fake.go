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

package fake

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
)

type Fake struct {
	events chan loops.Event
	loop   loop.Interface[loops.Event]
}

func New(t *testing.T) *Fake {
	t.Helper()

	f := &Fake{events: make(chan loops.Event, 10)}
	f.loop = loop.New[loops.Event](5).NewLoop(f)

	errCh := make(chan error)
	go func() { errCh <- f.loop.Run(t.Context()) }()

	t.Cleanup(func() {
		f.loop.Close(nil)
		select {
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout waiting for loop to finish")
		case err := <-errCh:
			require.NoError(t, err)
		}
	})

	return f
}

func (f *Fake) Handle(_ context.Context, event loops.Event) error {
	f.events <- event
	return nil
}

func (f *Fake) Events() <-chan loops.Event {
	return f.events
}

func (f *Fake) Loop() loop.Interface[loops.Event] {
	return f.loop
}
