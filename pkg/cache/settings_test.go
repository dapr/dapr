// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSettings(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		expect   Settings
		wantErr  bool
	}{
		{
			name:     "disable cache",
			metadata: map[string]string{"cacheEnable": "false"},
			expect:   Settings{EnableCache: false, TTL: defaultTTL, MemoryLimit: defaultMemoryLimit},
			wantErr:  false,
		},
		{
			name:     "enable cache",
			metadata: map[string]string{"cacheEnable": "true", "cacheTTL": "1s", "cacheMemoryLimit": "1000"},
			expect:   Settings{EnableCache: true, TTL: time.Second, MemoryLimit: 1000},
			wantErr:  false,
		},
		{
			name:     "err cacheEnable config",
			metadata: map[string]string{"cacheEnable": "cache"},
			wantErr:  true,
		},
		{
			name:     "err cacheTTL config",
			metadata: map[string]string{"cacheTTL": "s"},
			wantErr:  true,
		},
		{
			name:     "err cacheMemoryLimit config",
			metadata: map[string]string{"cacheMemoryLimit": "s"},
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := DefaultSettings()
			err := s.Decode(tt.metadata)
			assert.Equal(t, err != nil, tt.wantErr)
			if !tt.wantErr {
				assert.Equal(t, *s, tt.expect)
			}
		})
	}
}

func TestSettings_NewCache(t *testing.T) {
	metadata := map[string]string{"cacheEnable": "true", "cacheTTL": "1s", "cacheMemoryLimit": "1000"}
	s := DefaultSettings()
	err := s.Decode(metadata)
	assert.Nil(t, err)

	cache, err := s.NewCache()
	assert.Nil(t, err)
	assert.NotNil(t, cache)
	assert.NotNil(t, cache.Setting)
	assert.NotNil(t, cache.Cache)
	assert.NotNil(t, cache.aead)

	metadata = map[string]string{"cacheEnable": "false"}
	s = DefaultSettings()
	err = s.Decode(metadata)
	assert.Nil(t, err)

	cache, err = s.NewCache()
	assert.Equal(t, ErrDisable, err)
	assert.Nil(t, cache)
}

func Test_randomKey(t *testing.T) {
	key, err := randomKey()
	assert.Empty(t, err)
	assert.Equal(t, len(key), 32)
}
