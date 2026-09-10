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

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
)

func TestClampDrainOngoingCallTimeout(t *testing.T) {
	tests := map[string]struct {
		drain       time.Duration
		budget      time.Duration
		want        time.Duration
		wantClamped bool
	}{
		"drain below budget is unchanged": {
			drain:       10 * time.Second,
			budget:      30 * time.Second,
			want:        10 * time.Second,
			wantClamped: false,
		},
		"drain equal to budget is clamped to 80%": {
			drain:       30 * time.Second,
			budget:      30 * time.Second,
			want:        24 * time.Second,
			wantClamped: true,
		},
		"drain above budget is clamped to 80%": {
			drain:       60 * time.Second,
			budget:      30 * time.Second,
			want:        24 * time.Second,
			wantClamped: true,
		},
		"clamp floored at default ongoing call timeout when budget tiny": {
			drain:       60 * time.Second,
			budget:      2 * time.Second,
			want:        DefaultOngoingCallTimeout,
			wantClamped: true,
		},
		"zero budget disables clamp": {
			drain:       60 * time.Second,
			budget:      0,
			want:        60 * time.Second,
			wantClamped: false,
		},
		"negative budget disables clamp": {
			drain:       60 * time.Second,
			budget:      -1 * time.Second,
			want:        60 * time.Second,
			wantClamped: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, clamped := ClampDrainOngoingCallTimeout(tc.drain, tc.budget)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantClamped, clamped)
		})
	}
}

func TestTranslateEntityConfig(t *testing.T) {
	t.Run("drain timeout is stored as configured, not clamped", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:                []string{"foo"},
			DrainOngoingCallTimeout: "60s",
		})
		require.NotNil(t, got.DrainOngoingCallTimeout)
		assert.Equal(t, 60*time.Second, *got.DrainOngoingCallTimeout)
	})

	t.Run("unset drain timeout leaves nil so global applies", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities: []string{"foo"},
		})
		assert.Nil(t, got.DrainOngoingCallTimeout)
	})

	t.Run("invalid drain timeout leaves nil so global applies", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:                []string{"foo"},
			DrainOngoingCallTimeout: "not-a-duration",
		})
		assert.Nil(t, got.DrainOngoingCallTimeout)
	})

	t.Run("drain rebalanced actors override is preserved", func(t *testing.T) {
		f := false
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:              []string{"foo"},
			DrainRebalancedActors: &f,
		})
		require.NotNil(t, got.DrainRebalancedActors)
		assert.False(t, *got.DrainRebalancedActors)
	})

	t.Run("valid idle timeout is parsed", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:         []string{"foo"},
			ActorIdleTimeout: "2h",
		})
		assert.Equal(t, 2*time.Hour, got.ActorIdleTimeout)
	})

	t.Run("invalid idle timeout uses default", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:         []string{"foo"},
			ActorIdleTimeout: "not-a-duration",
		})
		assert.Equal(t, DefaultIdleTimeout, got.ActorIdleTimeout)
	})
}
