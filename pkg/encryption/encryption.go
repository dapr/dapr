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
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	b64 "encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/dapr/components-contrib/secretstores"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

type Algorithm string

const (
	primaryEncryptionKey   = "primaryEncryptionKey"
	secondaryEncryptionKey = "secondaryEncryptionKey"
	errPrefix              = "failed to extract encryption key"
	AESGCMAlgorithm        = "AES-GCM"
)

// ComponentEncryptionKeys holds the encryption keys set for a component.
type ComponentEncryptionKeys struct {
	Primary   Key
	Secondary Key
}

// Key holds the key to encrypt an arbitrary object.
type Key struct {
	Key       string
	Name      string
	cipherObj cipher.AEAD
}

// ComponentEncryptionKey checks if a component definition contains an encryption key and extracts it using the supplied secret store.
func ComponentEncryptionKey(component v1alpha1.Component, secretStore secretstores.SecretStore) (ComponentEncryptionKeys, error) {
	if secretStore == nil {
		return ComponentEncryptionKeys{}, nil
	}

	var cek ComponentEncryptionKeys

	for _, m := range component.Spec.Metadata {
		// search for primary encryption key
		var valid bool

		if m.Name == primaryEncryptionKey {
			if len(m.Value.Raw) > 0 {
				// encryption key is already extracted by the Operator
				cek.Primary = Key{
					Key:  m.Value.String(),
					Name: m.SecretKeyRef.Name,
				}

				continue
			}

			valid = true
		} else if m.Name == secondaryEncryptionKey {
			if len(m.Value.Raw) > 0 {
				cek.Secondary = Key{
					Key:  m.Value.String(),
					Name: m.SecretKeyRef.Name,
				}

				continue
			}

			valid = true
		}

		if !valid {
			continue
		}

		key, err := tryGetEncryptionKeyFromMetadataItem(component.Namespace, m, secretStore)
		if err != nil {
			return ComponentEncryptionKeys{}, fmt.Errorf("%s: %w", errPrefix, err)
		}

		if m.Name == primaryEncryptionKey {
			cek.Primary = key
		} else if m.Name == secondaryEncryptionKey {
			cek.Secondary = key
		}
	}

	if cek.Primary.Key != "" {
		cipherObj, err := createCipher(cek.Primary, AESGCMAlgorithm)
		if err != nil {
			return ComponentEncryptionKeys{}, err
		}

		cek.Primary.cipherObj = cipherObj
	}
	if cek.Secondary.Key != "" {
		cipherObj, err := createCipher(cek.Secondary, AESGCMAlgorithm)
		if err != nil {
			return ComponentEncryptionKeys{}, err
		}

		cek.Secondary.cipherObj = cipherObj
	}

	return cek, nil
}

func tryGetEncryptionKeyFromMetadataItem(namespace string, item commonapi.NameValuePair, secretStore secretstores.SecretStore) (Key, error) {
	if item.SecretKeyRef.Name == "" {
		return Key{}, fmt.Errorf("%s: secretKeyRef cannot be empty", errPrefix)
	}

	// TODO: cascade context.
	r, err := secretStore.GetSecret(context.TODO(), secretstores.GetSecretRequest{
		Name: item.SecretKeyRef.Name,
		Metadata: map[string]string{
			"namespace": namespace,
		},
	})
	if err != nil {
		return Key{}, fmt.Errorf("%s: %w", errPrefix, err)
	}

	key := item.SecretKeyRef.Key
	if key == "" {
		key = item.SecretKeyRef.Name
	}

	if val, ok := r.Data[key]; ok {
		if val == "" {
			return Key{}, fmt.Errorf("%s: encryption key cannot be empty", errPrefix)
		}

		return Key{
			Key:  r.Data[key],
			Name: item.SecretKeyRef.Name,
		}, nil
	}

	return Key{}, nil
}

// Encrypt takes a byte array and encrypts it using a supplied encryption key.
func encrypt(value []byte, key Key) ([]byte, error) {
	nsize := make([]byte, key.cipherObj.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nsize); err != nil {
		return value, err
	}

	return key.cipherObj.Seal(nsize, nsize, value, nil), nil
}

// Decrypt takes a byte array and decrypts it using a supplied encryption key.
func decrypt(value []byte, key Key) ([]byte, error) {
	enc, err := b64.StdEncoding.DecodeString(string(value))
	if err != nil {
		return value, err
	}

	nsize := key.cipherObj.NonceSize()
	nonce, ciphertext := enc[:nsize], enc[nsize:]

	return key.cipherObj.Open(nil, nonce, ciphertext, nil)
}

func createCipher(key Key, algorithm Algorithm) (cipher.AEAD, error) {
	keyBytes, err := hex.DecodeString(key.Key)
	if err != nil {
		return nil, err
	}

	switch algorithm {
	// Other authenticated ciphers can be added if needed, e.g. golang.org/x/crypto/chacha20poly1305
	case AESGCMAlgorithm:
		block, err := aes.NewCipher(keyBytes)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	}

	return nil, errors.New("unsupported algorithm")
}
