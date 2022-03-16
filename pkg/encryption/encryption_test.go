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
	"encoding/hex"
	"testing"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockSecretStore struct {
	secretstores.SecretStore
	primaryKey   string
	secondaryKey string
}

func (m *mockSecretStore) Init(metadata secretstores.Metadata) error {
	if val, ok := metadata.Properties["primaryKey"]; ok {
		m.primaryKey = val
	}

	if val, ok := metadata.Properties["secondaryKey"]; ok {
		m.secondaryKey = val
	}

	return nil
}

func (m *mockSecretStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"primaryKey":   m.primaryKey,
			"secondaryKey": m.secondaryKey,
		},
	}, nil
}

func (m *mockSecretStore) BulkGetSecret(req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	return secretstores.BulkGetSecretResponse{}, nil
}

func TestComponentEncryptionKey(t *testing.T) {
	t.Run("component has a primary and secondary encryption keys", func(t *testing.T) {
		component := v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "statestore",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []v1alpha1.MetadataItem{
					{
						Name: primaryEncryptionKey,
						SecretKeyRef: v1alpha1.SecretKeyRef{
							Name: "primaryKey",
						},
					},
					{
						Name: secondaryEncryptionKey,
						SecretKeyRef: v1alpha1.SecretKeyRef{
							Name: "secondaryKey",
						},
					},
				},
			},
		}

		bytes := make([]byte, 32)
		rand.Read(bytes)

		primaryKey := hex.EncodeToString(bytes)

		rand.Read(bytes)

		secondaryKey := hex.EncodeToString(bytes[:16]) // 128-bit key

		secretStore := &mockSecretStore{}
		secretStore.Init(secretstores.Metadata{
			Properties: map[string]string{
				"primaryKey":   primaryKey,
				"secondaryKey": secondaryKey,
			},
		})

		keys, err := ComponentEncryptionKey(component, secretStore)
		assert.NoError(t, err)
		assert.Equal(t, primaryKey, keys.Primary.Key)
		assert.Equal(t, secondaryKey, keys.Secondary.Key)
	})

	t.Run("keys empty when no secret store is present and no error", func(t *testing.T) {
		component := v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "statestore",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []v1alpha1.MetadataItem{
					{
						Name: primaryEncryptionKey,
						SecretKeyRef: v1alpha1.SecretKeyRef{
							Name: "primaryKey",
						},
					},
					{
						Name: secondaryEncryptionKey,
						SecretKeyRef: v1alpha1.SecretKeyRef{
							Name: "secondaryKey",
						},
					},
				},
			},
		}

		keys, err := ComponentEncryptionKey(component, nil)
		assert.Empty(t, keys.Primary.Key)
		assert.Empty(t, keys.Secondary.Key)
		assert.NoError(t, err)
	})

	t.Run("no error when component doesn't have encryption keys", func(t *testing.T) {
		component := v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "statestore",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []v1alpha1.MetadataItem{
					{
						Name: "something",
					},
				},
			},
		}

		_, err := ComponentEncryptionKey(component, nil)
		assert.NoError(t, err)
	})
}

func TestTryGetEncryptionKeyFromMetadataItem(t *testing.T) {
	t.Run("no secretRef on valid item", func(t *testing.T) {
		secretStore := &mockSecretStore{}
		secretStore.Init(secretstores.Metadata{
			Properties: map[string]string{
				"primaryKey":   "123",
				"secondaryKey": "456",
			},
		})

		_, err := tryGetEncryptionKeyFromMetadataItem("", v1alpha1.MetadataItem{}, secretStore)
		assert.Error(t, err)
	})
}

func TestCreateCipher(t *testing.T) {
	t.Run("invalid key", func(t *testing.T) {
		cipherObj, err := createCipher(Key{
			Key: "123",
		}, AESGCMAlgorithm)

		assert.Nil(t, cipherObj)
		assert.Error(t, err)
	})

	t.Run("valid 256-bit key", func(t *testing.T) {
		bytes := make([]byte, 32)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		cipherObj, err := createCipher(Key{
			Key: key,
		}, AESGCMAlgorithm)

		assert.NotNil(t, cipherObj)
		assert.NoError(t, err)
	})

	t.Run("valid 192-bit key", func(t *testing.T) {
		bytes := make([]byte, 24)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		cipherObj, err := createCipher(Key{
			Key: key,
		}, AESGCMAlgorithm)

		assert.NotNil(t, cipherObj)
		assert.NoError(t, err)
	})

	t.Run("valid 128-bit key", func(t *testing.T) {
		bytes := make([]byte, 16)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		cipherObj, err := createCipher(Key{
			Key: key,
		}, AESGCMAlgorithm)

		assert.NotNil(t, cipherObj)
		assert.NoError(t, err)
	})

	t.Run("invalid key size", func(t *testing.T) {
		bytes := make([]byte, 18)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		cipherObj, err := createCipher(Key{
			Key: key,
		}, AESGCMAlgorithm)

		assert.Nil(t, cipherObj)
		assert.Error(t, err)
	})

	t.Run("invalid algorithm", func(t *testing.T) {
		bytes := make([]byte, 32)
		rand.Read(bytes)

		key := hex.EncodeToString(bytes)

		cipherObj, err := createCipher(Key{
			Key: key,
		}, "3DES")

		assert.Nil(t, cipherObj)
		assert.Error(t, err)
	})
}
