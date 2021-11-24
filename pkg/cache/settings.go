// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package cache

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/dapr/kit/config"

	"github.com/dgraph-io/ristretto"
)

var ErrDisable = errors.New("cache is disabled")

const (
	MaxMemoryLimit     = 100 * 1024 * 1024 // 100M
	defaultMemoryLimit = 1 * 1024 * 1024   // 1M
	defaultTTL         = 5 * time.Minute   // 5min
)

type Settings struct {
	EnableCache bool          `mapstructure:"cacheEnable"`
	TTL         time.Duration `mapstructure:"cacheTTL"`
	MemoryLimit int64         `mapstructure:"cacheMemoryLimit"`
}

func DefaultSettings() *Settings {
	return &Settings{
		MemoryLimit: defaultMemoryLimit,
		TTL:         defaultTTL,
	}
}

func (s *Settings) Decode(in interface{}) error {
	if err := config.Decode(in, s); err != nil {
		return fmt.Errorf("decode failed. %w", err)
	}

	return nil
}

func (s *Settings) IsEnabled() bool {
	return s.EnableCache
}

func (s *Settings) NewCache() (*Cache, error) {
	if !s.IsEnabled() {
		return nil, ErrDisable
	}
	if s.MemoryLimit > MaxMemoryLimit {
		s.MemoryLimit = MaxMemoryLimit
	}
	cacheConfig := &ristretto.Config{
		NumCounters: s.MemoryLimit / 10, // num of max items, can be a metadata setting if needed
		MaxCost:     s.MemoryLimit,
		BufferItems: 64,
		Metrics:     true,
	}

	c, err := ristretto.NewCache(cacheConfig)
	if err != nil {
		return nil, fmt.Errorf("generate cache instance err: %w", err)
	}

	// since the data is in memory so just use a random key
	key, err := randomKey()
	if err != nil {
		return nil, fmt.Errorf("generate random key err: %w", err)
	}

	gcm, err := createCipher(key)
	if err != nil {
		return nil, fmt.Errorf("generate cipher err: %w", err)
	}

	return &Cache{
		Setting: s,
		Cache:   c,
		aead:    gcm,
	}, nil
}

func randomKey() (key []byte, err error) {
	key = make([]byte, 32)
	for i := 0; i < 10; i++ {
		_, err = rand.Read(key)
		if err == nil {
			break
		}
	}
	return key, err
}

func createCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	return cipher.NewGCM(block)
}
