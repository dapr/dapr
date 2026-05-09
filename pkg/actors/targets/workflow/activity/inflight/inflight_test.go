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

package inflight_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
)

func TestAcquireFirstCallerIsOwner(t *testing.T) {
	var m inflight.Map

	call, owner := m.Acquire("a1")
	require.NotNil(t, call)
	require.True(t, owner)

	call2, owner2 := m.Acquire("a1")
	require.Same(t, call, call2)
	require.False(t, owner2)
}

func TestFollowerObservesOwnerOutcome(t *testing.T) {
	var m inflight.Map

	owner, isOwner := m.Acquire("a1")
	require.True(t, isOwner)

	follower, isOwner := m.Acquire("a1")
	require.False(t, isOwner)

	select {
	case <-follower.Done():
		t.Fatal("follower observed Done before owner Finish")
	default:
	}

	wantErr := errors.New("boom")
	owner.Finish(wantErr)

	select {
	case <-follower.Done():
	case <-time.After(time.Second):
		t.Fatal("follower did not observe Done after owner Finish")
	}

	assert.Same(t, wantErr, follower.Err())
}

func TestFollowerSurfacesNilOnSuccess(t *testing.T) {
	var m inflight.Map

	owner, _ := m.Acquire("a1")
	follower, _ := m.Acquire("a1")

	owner.Finish(nil)

	<-follower.Done()
	assert.NoError(t, follower.Err())
}

func TestManyFollowersAllSeeSameOutcome(t *testing.T) {
	var m inflight.Map

	owner, isOwner := m.Acquire("a1")
	require.True(t, isOwner)

	const followers = 50
	var seen atomic.Int32
	var wg sync.WaitGroup
	wg.Add(followers)
	for range followers {
		go func() {
			defer wg.Done()
			f, fowner := m.Acquire("a1")
			assert.False(t, fowner)
			<-f.Done()
			assert.NoError(t, f.Err())
			seen.Add(1)
		}()
	}

	owner.Finish(nil)
	wg.Wait()

	assert.Equal(t, int32(followers), seen.Load())
}

func TestFinishIsIdempotent(t *testing.T) {
	var m inflight.Map
	c, _ := m.Acquire("a1")

	first := errors.New("first")
	c.Finish(first)
	c.Finish(errors.New("second"))

	<-c.Done()
	assert.Same(t, first, c.Err())
}

func TestReleaseAllowsNewOwner(t *testing.T) {
	var m inflight.Map

	c1, owner := m.Acquire("a1")
	require.True(t, owner)

	m.Release("a1", c1)

	c2, owner := m.Acquire("a1")
	require.True(t, owner)
	require.NotSame(t, c1, c2)
}

func TestReleaseDoesNotClobberFollowOnEntry(t *testing.T) {
	var m inflight.Map

	c1, _ := m.Acquire("a1")
	c1.Finish(nil)
	m.Release("a1", c1)

	c2, owner := m.Acquire("a1")
	require.True(t, owner)
	require.NotSame(t, c1, c2)

	m.Release("a1", c1)

	current, follower := m.Acquire("a1")
	require.Same(t, c2, current, "stale Release must not delete the live entry")
	require.False(t, follower)
}

func TestReleaseAfterRemovesEntry(t *testing.T) {
	var m inflight.Map

	c1, _ := m.Acquire("a1")
	c1.Finish(nil)

	m.ReleaseAfter("a1", c1, 50*time.Millisecond)

	require.Eventually(t, func() bool {
		c, owner := m.Acquire("a1")
		if owner {
			m.Release("a1", c)
			return true
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)
}

func TestFollowerCtxCancelDoesNotAffectOwner(t *testing.T) {
	var m inflight.Map

	owner, _ := m.Acquire("a1")
	follower, isOwner := m.Acquire("a1")
	require.False(t, isOwner)

	owner.Finish(nil)

	<-follower.Done()
	assert.NoError(t, follower.Err())
	assert.NoError(t, owner.Err())
}
