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
		drain  time.Duration
		dissem time.Duration
		want   time.Duration
	}{
		"drain below dissemination is unchanged": {
			drain:  10 * time.Second,
			dissem: 30 * time.Second,
			want:   10 * time.Second,
		},
		"drain equal to dissemination is clamped to 80%": {
			drain:  30 * time.Second,
			dissem: 30 * time.Second,
			want:   24 * time.Second,
		},
		"drain above dissemination is clamped to 80%": {
			drain:  60 * time.Second,
			dissem: 30 * time.Second,
			want:   24 * time.Second,
		},
		"clamp floored at default ongoing call timeout when dissemination tiny": {
			drain:  60 * time.Second,
			dissem: 2 * time.Second,
			want:   DefaultOngoingCallTimeout,
		},
		"zero dissemination disables clamp": {
			drain:  60 * time.Second,
			dissem: 0,
			want:   60 * time.Second,
		},
		"negative dissemination disables clamp": {
			drain:  60 * time.Second,
			dissem: -1 * time.Second,
			want:   60 * time.Second,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := ClampDrainOngoingCallTimeout(tc.drain, tc.dissem, "test")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTranslateEntityConfig(t *testing.T) {
	t.Run("drain timeout below dissemination passes through", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:                []string{"foo"},
			DrainOngoingCallTimeout: "10s",
		}, 30*time.Second)
		require.NotNil(t, got.DrainOngoingCallTimeout)
		assert.Equal(t, 10*time.Second, *got.DrainOngoingCallTimeout)
	})

	t.Run("drain timeout above dissemination is clamped", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:                []string{"foo"},
			DrainOngoingCallTimeout: "60s",
		}, 30*time.Second)
		require.NotNil(t, got.DrainOngoingCallTimeout)
		assert.Equal(t, 24*time.Second, *got.DrainOngoingCallTimeout)
	})

	t.Run("unset drain timeout leaves nil so global applies", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities: []string{"foo"},
		}, 30*time.Second)
		assert.Nil(t, got.DrainOngoingCallTimeout)
	})

	t.Run("invalid drain timeout leaves nil so global applies", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:                []string{"foo"},
			DrainOngoingCallTimeout: "not-a-duration",
		}, 30*time.Second)
		assert.Nil(t, got.DrainOngoingCallTimeout)
	})

	t.Run("valid idle timeout is parsed", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:         []string{"foo"},
			ActorIdleTimeout: "2h",
		}, 30*time.Second)
		assert.Equal(t, 2*time.Hour, got.ActorIdleTimeout)
	})

	t.Run("invalid idle timeout uses default", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:         []string{"foo"},
			ActorIdleTimeout: "not-a-duration",
		}, 30*time.Second)
		assert.Equal(t, DefaultIdleTimeout, got.ActorIdleTimeout)
	})

	t.Run("zero dissemination timeout disables clamp", func(t *testing.T) {
		got := TranslateEntityConfig(config.EntityConfig{
			Entities:                []string{"foo"},
			DrainOngoingCallTimeout: "60s",
		}, 0)
		require.NotNil(t, got.DrainOngoingCallTimeout)
		assert.Equal(t, 60*time.Second, *got.DrainOngoingCallTimeout)
	})
}
