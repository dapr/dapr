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

package wfengine

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/table"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
)

// standbyTable records the registrations the engine makes.
type standbyTable struct {
	lock   sync.Mutex
	events []string

	// tableCtx is the ctx of the last actors.Table call, and standbyCtx the
	// one the last standby registration was made with.
	tableCtx   context.Context
	standbyCtx context.Context
}

func (s *standbyTable) lastStandbyCtx() context.Context {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.standbyCtx
}

func (s *standbyTable) record(e string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.events = append(s.events, e)
}

func (s *standbyTable) take() []string {
	s.lock.Lock()
	defer s.lock.Unlock()
	e := s.events
	s.events = nil
	return e
}

func newStandbyTestEngine(t *testing.T, standbyHosts bool) (*engine, *standbyTable) {
	t.Helper()

	st := new(standbyTable)
	tbl := tablefake.New().
		WithRegisterActorTypes(func(opts table.RegisterActorTypeOptions) {
			if opts.Standby {
				st.lock.Lock()
				st.standbyCtx = st.tableCtx
				st.lock.Unlock()
				st.record("register-standby")
			} else {
				st.record("register")
			}
		}).
		WithUnRegisterActorTypes(func(...string) error {
			st.record("unregister")
			return nil
		})
	fa := actorsfake.New().WithTable(func(ctx context.Context) (table.Interface, error) {
		st.lock.Lock()
		st.tableCtx = ctx
		st.lock.Unlock()
		return tbl, nil
	})

	wfe, _ := newTestEngine(t, fa)
	wfe.standbyHosts = standbyHosts

	// Mirror Run.
	wfe.actorRegLock.Lock()
	wfe.runCtx = t.Context()
	wfe.registerStandby()
	wfe.actorRegLock.Unlock()

	return wfe, st
}

func TestEngine_StandbyTransitions(t *testing.T) {
	t.Run("standby until a worker connects, and again after the last leaves", func(t *testing.T) {
		wfe, st := newStandbyTestEngine(t, true)
		assert.Equal(t, []string{"register-standby"}, st.take())
		assert.True(t, wfe.standbyRegistered)
		assert.False(t, wfe.actorsRegistered)

		// The first worker replaces the standby host with the active one,
		// halting the standby actors first.
		require.NoError(t, wfe.onWorkItemConnection(t.Context()))
		assert.Equal(t, []string{"unregister", "register"}, st.take())
		assert.False(t, wfe.standbyRegistered)
		assert.True(t, wfe.actorsRegistered)

		// A second worker changes nothing.
		require.NoError(t, wfe.onWorkItemConnection(t.Context()))
		require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
		assert.Empty(t, st.take())

		// The last worker leaving unregisters, then registers standby.
		require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
		assert.Equal(t, []string{"unregister", "register-standby"}, st.take())
		assert.True(t, wfe.standbyRegistered)
		assert.False(t, wfe.actorsRegistered)
	})

	t.Run("each standby registration's context ends once it is unregistered", func(t *testing.T) {
		wfe, st := newStandbyTestEngine(t, true)
		first := st.lastStandbyCtx()
		require.NotNil(t, first)
		require.NoError(t, first.Err())

		// The standby factory's background goroutines stop with the worker
		// connecting, not with Run's context.
		require.NoError(t, wfe.onWorkItemConnection(t.Context()))
		require.ErrorIs(t, first.Err(), context.Canceled)
		require.NoError(t, wfe.runCtx.Err())
		assert.Nil(t, wfe.standbyCancel)

		// The next standby registration gets a fresh context.
		require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
		second := st.lastStandbyCtx()
		require.NotSame(t, first, second)
		require.NoError(t, second.Err())
		assert.NotNil(t, wfe.standbyCancel)
	})

	t.Run("feature disabled never registers standby", func(t *testing.T) {
		wfe, st := newStandbyTestEngine(t, false)
		assert.Empty(t, st.take())

		require.NoError(t, wfe.onWorkItemConnection(t.Context()))
		require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
		assert.Equal(t, []string{"register", "unregister"}, st.take())
		assert.False(t, wfe.standbyRegistered)
		assert.False(t, wfe.actorsRegistered)
	})

	t.Run("no standby registration once Run's context is done", func(t *testing.T) {
		wfe, st := newStandbyTestEngine(t, true)
		require.NoError(t, wfe.onWorkItemConnection(t.Context()))
		st.take()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		wfe.actorRegLock.Lock()
		wfe.runCtx = ctx
		wfe.actorRegLock.Unlock()

		require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
		assert.Equal(t, []string{"unregister"}, st.take())
		assert.False(t, wfe.standbyRegistered)
	})
}
