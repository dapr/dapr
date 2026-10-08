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

package executor

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
)

func Test_Deactivate_concurrentInvocations(t *testing.T) {
	t.Parallel()

	for i := range 300 {
		f := newTestFactory(t)
		e := f.GetOrCreate(testActorID).(*executor)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 4 {
			wg.Go(func() {
				<-start
				_, err := e.InvokeMethod(t.Context(), methodReq(MethodRegister))
				if err != nil {
					assert.True(t, targeterrors.IsClosed(err), "iteration %d: %v", i, err)
				}
			})
		}
		wg.Go(func() {
			<-start
			assert.NoError(t, e.Deactivate(t.Context()), "iteration %d", i)
		})
		close(start)
		wg.Wait()

		assert.True(t, e.isClosed(), "iteration %d", i)
		assert.False(t, f.Exists(testActorID), "iteration %d", i)
	}
}

func Test_Deactivate_refusesLaterCalls(t *testing.T) {
	t.Parallel()

	f := newTestFactory(t)
	e := f.GetOrCreate(testActorID).(*executor)
	require.NoError(t, e.Deactivate(t.Context()))

	_, err := e.InvokeMethod(t.Context(), methodReq("unknown"))
	assert.True(t, targeterrors.IsClosed(err), "got %v", err)

	err = e.InvokeStream(t.Context(), methodReq("unknown"), nil)
	assert.True(t, targeterrors.IsClosed(err), "got %v", err)
}
