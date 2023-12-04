/*
Copyright 2021 The Dapr Authors
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

package encryption

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddEncryptedStateStore(t *testing.T) {
	t.Run("state store doesn't exist", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		r := AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: Key{
				Name: "primary",
				Key:  "1234",
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
				Key:  "1234",
			},
		})

		assert.True(t, r)
		assert.Equal(t, "primary", encryptedStateStores["test"].Primary.Name)

		r = AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: Key{
				Name: "primary",
				Key:  "1234",
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

		cipherObj, _ := createCipher(pr, AESGCMAlgorithm)
		pr.cipherObj = cipherObj

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
		})

		v := []byte("hello")
		r, err := TryEncryptValue("test", v)

		require.NoError(t, err)
		assert.NotEqual(t, v, r)

		dr, err := TryDecryptValue("test", r)
		require.NoError(t, err)
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

		cipherObj, _ := createCipher(pr, AESGCMAlgorithm)
		pr.cipherObj = cipherObj

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
		})

		v := []byte("hello")
		r, err := TryEncryptValue("test", v)

		require.NoError(t, err)
		assert.NotEqual(t, v, r)

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Secondary: pr,
		})

		dr, err := TryDecryptValue("test", r)
		require.NoError(t, err)
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

		cipherObj, _ := createCipher(pr, AESGCMAlgorithm)
		pr.cipherObj = cipherObj

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
		})

		v := []byte("hello")
		s := base64.StdEncoding.EncodeToString(v)
		r, err := TryEncryptValue("test", []byte(s))

		require.NoError(t, err)
		assert.NotEqual(t, v, r)

		dr, err := TryDecryptValue("test", r)
		require.NoError(t, err)
		assert.Equal(t, []byte(s), dr)
	})

	t.Run("state store with AES128 primary key, value encrypted and decrypted successfully", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}

		bytes := make([]byte, 16)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		pr := Key{
			Name: "primary",
			Key:  key,
		}

		cipherObj, _ := createCipher(pr, AESGCMAlgorithm)
		pr.cipherObj = cipherObj

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
		})

		v := []byte("hello world")
		r, err := TryEncryptValue("test", v)

		require.NoError(t, err)
		assert.NotEqual(t, v, r)

		dr, err := TryDecryptValue("test", r)
		require.NoError(t, err)
		assert.Equal(t, v, dr)
	})
}

func TestTryDecryptValue(t *testing.T) {
	t.Run("empty value", func(t *testing.T) {
		encryptedStateStores = map[string]ComponentEncryptionKeys{}

		bytes := make([]byte, 16)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		pr := Key{
			Name: "primary",
			Key:  key,
		}

		cipherObj, _ := createCipher(pr, AESGCMAlgorithm)
		pr.cipherObj = cipherObj

		encryptedStateStores = map[string]ComponentEncryptionKeys{}
		AddEncryptedStateStore("test", ComponentEncryptionKeys{
			Primary: pr,
		})

		dr, err := TryDecryptValue("test", nil)
		require.NoError(t, err)
		assert.Empty(t, dr)
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
