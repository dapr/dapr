// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package encryption

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddEncryptedStateStore(t *testing.T) {
	t.Run("state store doesn't exist", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		r := AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: Key{
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
			Primary: Key{
				Name: "primary",
				Key:  "123",
			},
		})

		assert.True(t, r)
		assert.Equal(t, "primary", encryptedStateStores["test"].Primary.Name)

		r = AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: Key{
				Name: "primary",
				Key:  "123",
			},
		})

		assert.False(t, r)
	})
}

func TestTryEncryptValue(t *testing.T) {
	t.Run("state store without keys", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		ok := EncryptedStateStore("test")
		assert.False(t, ok)
	})

	t.Run("state store with AES256 primary key, value encrypted and decrypted successfully", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}

		bytes := make([]byte, 32)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		pr := Key{
			Name: "primary",
			Key:  key,
		}

		gcm, _ := createCipher(pr, AES256Algorithm)
		pr.gcm = gcm

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
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

		pr := Key{
			Name: "primary",
			Key:  primaryKey,
		}

		gcm, _ := createCipher(pr, AES256Algorithm)
		pr.gcm = gcm

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
		})

		v := []byte("hello")
		r, err := TryEncryptValue("test", v)

		assert.NoError(t, err)
		assert.NotEqual(t, v, r)

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Secondary: pr,
		})

		dr, err := TryDecryptValue("test", r)
		assert.NoError(t, err)
		assert.Equal(t, v, dr)
	})

	t.Run("state store with AES256 primary key, base64 string value encrypted and decrypted successfully", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}

		bytes := make([]byte, 32)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		pr := Key{
			Name: "primary",
			Key:  key,
		}

		gcm, _ := createCipher(pr, AES256Algorithm)
		pr.gcm = gcm

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
		})

		v := []byte("hello")
		s := base64.StdEncoding.EncodeToString(v)
		r, err := TryEncryptValue("test", []byte(s))

		assert.NoError(t, err)
		assert.NotEqual(t, v, r)

		dr, err := TryDecryptValue("test", r)
		assert.NoError(t, err)
		assert.Equal(t, []byte(s), dr)
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
