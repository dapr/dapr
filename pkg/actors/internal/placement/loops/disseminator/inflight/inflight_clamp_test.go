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

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
)

func TestDrainBudget(t *testing.T) {
	t.Run("no advertised value uses daprd dissemination timeout", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		assert.Equal(t, 30*time.Second, i.drainBudget())
	})

	t.Run("lower advertised value lowers the budget", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		i.SetAdvertisedDisseminateTimeout(8 * time.Second)
		assert.Equal(t, 8*time.Second, i.drainBudget())
	})

	t.Run("higher advertised value does not raise the budget", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		i.SetAdvertisedDisseminateTimeout(60 * time.Second)
		assert.Equal(t, 30*time.Second, i.drainBudget())
	})

	t.Run("advertised value used when daprd timeout disabled", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		i.SetAdvertisedDisseminateTimeout(8 * time.Second)
		assert.Equal(t, 8*time.Second, i.drainBudget())
	})

	t.Run("non-positive advertised values are ignored", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		i.SetAdvertisedDisseminateTimeout(0)
		i.SetAdvertisedDisseminateTimeout(-time.Second)
		assert.Nil(t, i.advertisedDissTimeout.Load())
		assert.Equal(t, 30*time.Second, i.drainBudget())
	})
}

func TestClampDrain(t *testing.T) {
	t.Run("drain below budget passes through", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		assert.Equal(t, 10*time.Second, i.clampDrain(10*time.Second, "global config"))
	})

	t.Run("drain above budget is clamped to 80% of budget", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		assert.Equal(t, 24*time.Second, i.clampDrain(60*time.Second, "global config"))
	})

	t.Run("advertised placement timeout tightens the clamp", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		i.SetAdvertisedDisseminateTimeout(8 * time.Second)
		// 10s passes daprd's own 30s but exceeds the advertised 8s budget:
		// clamped to 80% of 8s.
		assert.Equal(t, 6400*time.Millisecond, i.clampDrain(10*time.Second, "global config"))
	})

	t.Run("clamp floored at default ongoing call timeout", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		i.SetAdvertisedDisseminateTimeout(2 * time.Second)
		assert.Equal(t, api.DefaultOngoingCallTimeout, i.clampDrain(10*time.Second, "global config"))
	})

	t.Run("warning bookkeeping tracks value changes per source", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})

		i.clampDrain(60*time.Second, "global config")
		v, ok := i.clampWarned.Load("global config")
		require.True(t, ok)
		assert.Equal(t, clampKey{drain: 60 * time.Second, budget: 30 * time.Second}, v)

		// Unclamped call clears the bookkeeping so a future clamp warns again.
		i.clampDrain(10*time.Second, "global config")
		_, ok = i.clampWarned.Load("global config")
		assert.False(t, ok)
	})
}

func TestClampedGlobalDrainTimeout(t *testing.T) {
	t.Run("nil when no global timeout configured", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		assert.Nil(t, i.clampedGlobalDrainTimeout())
	})

	t.Run("clamps the configured global timeout", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
		drain := true
		timeout := time.Minute
		i.SetDrainOngoingCallTimeout(&drain, &timeout)

		got := i.clampedGlobalDrainTimeout()
		require.NotNil(t, got)
		assert.Equal(t, 24*time.Second, *got)
	})
}

func TestPerTypeDrain(t *testing.T) {
	f, tr := false, true

	t.Run("nil when no entity configs", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		assert.Nil(t, i.perTypeDrain(nil))
		assert.Nil(t, i.perTypeDrain([]string{"a"}))
	})

	t.Run("filters to requested types", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		i.SetEntityDrainConfigs(map[string]api.EntityDrainConfig{
			"a": {DrainRebalancedActors: &f},
			"b": {DrainRebalancedActors: &tr},
			"c": {Timeout: &[]time.Duration{time.Second}[0]},
		})

		got := i.perTypeDrain([]string{"a", "c", "unknown"})
		assert.Equal(t, map[string]bool{"a": false}, got)
	})

	t.Run("nil types returns every configured override", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		i.SetEntityDrainConfigs(map[string]api.EntityDrainConfig{
			"a": {DrainRebalancedActors: &f},
			"b": {DrainRebalancedActors: &tr},
		})

		got := i.perTypeDrain(nil)
		assert.Equal(t, map[string]bool{"a": false, "b": true}, got)
	})
}

func TestCancelClaimsForTypes_EntityNoDrainCancelsImmediately(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
	i.Set(newTables(100, map[string]map[string]int64{
		"nodrain": {"h:1": 1},
	}), 1)
	i.Open(ctx)

	respCh := make(chan *loops.LockResponse, 1)
	i.Acquire(&loops.LockRequest{
		ActorType: "nodrain",
		Context:   ctx,
		Response:  respCh,
	})
	var resp *loops.LockResponse
	select {
	case resp = <-respCh:
	case <-time.After(time.Second):
		require.Fail(t, "Acquire should resolve")
	}

	f := false
	i.SetEntityDrainConfigs(map[string]api.EntityDrainConfig{
		"nodrain": {DrainRebalancedActors: &f},
	})

	start := time.Now()
	i.CancelClaimsForTypes([]string{"nodrain"}, errors.New("placement table updated"))
	assert.Less(t, time.Since(start), time.Second,
		"entity-level drainRebalancedActors=false must cancel immediately, not wait the drain window")

	select {
	case <-resp.Context.Done():
	case <-time.After(time.Second):
		require.Fail(t, "claim context should have been cancelled")
	}

	i.Close(nil)
}

func TestCancelClaimsForTypes_EntityDrainOverridesGlobalNoDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1", DisseminationTimeout: 30 * time.Second})
	i.Set(newTables(100, map[string]map[string]int64{
		"drains": {"h:1": 1},
	}), 1)
	i.Open(ctx)

	respCh := make(chan *loops.LockResponse, 1)
	i.Acquire(&loops.LockRequest{
		ActorType: "drains",
		Context:   ctx,
		Response:  respCh,
	})
	select {
	case <-respCh:
	case <-time.After(time.Second):
		require.Fail(t, "Acquire should resolve")
	}

	globalDrain := false
	globalTimeout := time.Minute
	i.SetDrainOngoingCallTimeout(&globalDrain, &globalTimeout)

	tr := true
	perTypeTimeout := 100 * time.Millisecond
	i.SetEntityDrainConfigs(map[string]api.EntityDrainConfig{
		"drains": {DrainRebalancedActors: &tr, Timeout: &perTypeTimeout},
	})

	start := time.Now()
	i.CancelClaimsForTypes([]string{"drains"}, errors.New("placement table updated"))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, perTypeTimeout,
		"entity-level drainRebalancedActors=true must drain for the per-type window despite the global opt-out")
	assert.Less(t, elapsed, 5*time.Second)

	i.Close(nil)
}
