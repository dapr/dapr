// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package encryption

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddEncryptedStateStore(t *testing.T) {
	t.Run("state store doesn't exist", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		r := AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: EncryptionKey{
				Name: "primary",
				Key:  "123",
			},
		})
		assert.True(t, r)
		assert.Equal(t, "primary", encryptedStateStores["test"].Primary.Name)
	})

	t.Run("state store exists", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		r := AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: EncryptionKey{
				Name: "primary",
				Key:  "123",
			},
		})

		assert.True(t, r)
		assert.Equal(t, "primary", encryptedStateStores["test"].Primary.Name)

		r = AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: EncryptionKey{
				Name: "primary",
				Key:  "123",
			},
		})

		assert.False(t, r)
	})
}

func TestTryEncryptValue(t *testing.T) {
	t.Run("state store without keys, value not encrypted", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		v := []byte("hello")
		r, err := TryEncryptValue("test", v)
		assert.NoError(t, err)
		assert.Equal(t, v, r)
	})

	t.Run("state store with non AES256 primary key, error returned", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: EncryptionKey{
				Name: "primary",
				Key:  "123",
			},
		})

		v := []byte("hello")
		r, err := TryEncryptValue("test", v)
		assert.Error(t, err)
		assert.Equal(t, v, r)
	})

	t.Run("state store with AES256 primary key, value encrypted and decrypted successfully", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}

		bytes := make([]byte, 32)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: EncryptionKey{
				Name: "primary",
				Key:  key,
			},
		})

		v := []byte("hello")
		r, err := TryEncryptValue("test", v)

		assert.NoError(t, err)
		assert.NotEqual(t, v, r)

		dr, err := TryDecryptValue("test", r)
		assert.NoError(t, err)
		assert.Equal(t, v, dr)
	})

	t.Run("state store with AES256 secondary key, value encrypted and decrypted successfully", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}

		bytes := make([]byte, 32)
		rand.Read(bytes)

		primaryKey := hex.EncodeToString(bytes)

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: EncryptionKey{
				Name: "primary",
				Key:  primaryKey,
			},
		})

		v := []byte("hello")
		r, err := TryEncryptValue("test", v)

		assert.NoError(t, err)
		assert.NotEqual(t, v, r)

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Secondary: EncryptionKey{
				Name: "primary",
				Key:  primaryKey,
			},
		})

		dr, err := TryDecryptValue("test", r)
		assert.NoError(t, err)
		assert.Equal(t, v, dr)
	})
}

func TestEncryptedStateStore(t *testing.T) {
	t.Run("store supports encryption", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{})
		ok := EncryptedStateStore("test")

		assert.True(t, ok)
	})

	t.Run("store doesn't support encryption", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		ok := EncryptedStateStore("test")

		assert.False(t, ok)
	})
}
