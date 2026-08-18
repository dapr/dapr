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

package lock

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/targets/errors"
)

func TestStallableReleaseStall(t *testing.T) {
	t.Parallel()

	l := NewStallable()

	unlock, err := l.ContextLock(t.Context())
	require.NoError(t, err)

	releaseCh, unstall := l.Stall()

	_, err = l.ContextLock(t.Context())
	require.True(t, errors.IsStalled(err))

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-releaseCh
		unstall()
		unlock()
	}()

	l.ReleaseStall()

	unlock, err = l.ContextLock(t.Context())
	require.NoError(t, err)
	unlock()
	<-done
}

func TestStallableReleaseStallNoHolder(t *testing.T) {
	t.Parallel()

	l := NewStallable()
	l.ReleaseStall()

	unlock, err := l.ContextLock(t.Context())
	require.NoError(t, err)
	unlock()
}

func TestStallableUnstallResetsStalledState(t *testing.T) {
	t.Parallel()

	l := NewStallable()

	unlock, err := l.ContextLock(t.Context())
	require.NoError(t, err)

	_, unstall := l.Stall()
	_, err = l.ContextLock(t.Context())
	require.True(t, errors.IsStalled(err))

	unstall()
	unlock()

	unlock, err = l.ContextLock(t.Context())
	require.NoError(t, err)
	unlock()
}
