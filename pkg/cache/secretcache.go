// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package cache

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dapr/components-contrib/secretstores"

	"github.com/dgraph-io/ristretto"
)

const (
	separator = "||"
	versionID = "version_id"
)

var (
	secretStoreCaches = map[string]*Cache{}

	ErrNotFound       = errors.New("cache not found")
	ErrSetCacheFailed = errors.New("set cache failed")
)

type Cache struct {
	Setting *Settings
	Cache   *ristretto.Cache
	aead    cipher.AEAD
}

func EnabledForSecretStore(storeName string) bool {
	_, ok := secretStoreCaches[storeName]
	return ok
}

func InitSecretStoreCaches(storeName string, metadata map[string]string) error {
	settings := DefaultSettings()
	err := settings.Decode(metadata)
	if err != nil {
		return fmt.Errorf("init secret store cache err: %w", err)
	}
	if !settings.EnableCache {
		return nil
	}
	cache, err := settings.NewCache()
	if err != nil {
		return fmt.Errorf("init secret store creating cache err: %w", err)
	}

	secretStoreCaches[storeName] = cache
	return nil
}

// getCacheKey get cache key for a secret request, support version.
func getCacheKey(req secretstores.GetSecretRequest) string {
	if req.Metadata != nil && req.Metadata[versionID] != "" {
		return req.Name + separator + req.Metadata[versionID]
	}
	return req.Name
}

// GetValue get a secret value from cache.
func GetValue(storeName string, req secretstores.GetSecretRequest) (res map[string]string, err error) {
	key := getCacheKey(req)
	c := secretStoreCaches[storeName]
	v, ok := c.Cache.Get(key)
	if !ok {
		return nil, ErrNotFound
	}
	b := v.([]byte)
	value, err := decrypt(b, c.aead)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(value, &res)
	if err != nil {
		return nil, err
	}
	return
}

// SetValueSync set a cache value synchronously.
func SetValueSync(storeName string, req secretstores.GetSecretRequest, value map[string]string) error {
	return SetValue(storeName, req, value, true)
}

// SetValueAsync  set a cache value asynchronously for better performance.
func SetValueAsync(storeName string, req secretstores.GetSecretRequest, value map[string]string) error {
	return SetValue(storeName, req, value, false)
}

// SetValue set secret key value to cache.
func SetValue(storeName string, req secretstores.GetSecretRequest, value map[string]string, sync bool) error {
	key := getCacheKey(req)
	// empty value will not cached
	if len(value) == 0 {
		return nil
	}
	c := secretStoreCaches[storeName]
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encrypted, err := encrypt(bytes, c.aead)
	if err != nil {
		return err
	}
	cost := len(encrypted)

	ttl := c.Setting.TTL
	if c.Setting.TTL <= 0 {
		ttl = 0
	}

	success := c.Cache.SetWithTTL(key, encrypted, int64(cost), ttl)
	if !success {
		return ErrSetCacheFailed
	}
	if sync {
		// wait until success
		c.Cache.Wait()
	}
	return nil
}

// Encrypt takes a byte array and encrypts it using a supplied encryption key and algorithm.
func encrypt(value []byte, aead cipher.AEAD) ([]byte, error) {
	nsize := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nsize); err != nil {
		return value, err
	}

	return aead.Seal(nsize, nsize, value, nil), nil
}

// Decrypt takes a byte array and decrypts it using a supplied encryption key and algorithm.
func decrypt(value []byte, aead cipher.AEAD) ([]byte, error) {
	nsize := aead.NonceSize()
	nonce, ciphertext := value[:nsize], value[nsize:]

	return aead.Open(nil, nonce, ciphertext, nil)
}
