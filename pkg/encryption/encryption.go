// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"io"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

type Algorithm string

const (
	primaryEncryptionKey   = "primaryEncryptionKey"
	secondaryEncryptionKey = "secondaryEncryptionKey"
	errPrefix              = "failed to extract encryption key"
	AES256Algorithm        = "AES256"
)

// ComponentEncryptionKeys holds the encryption keys set for a component.
type ComponentEncryptionKeys struct {
	Primary   Key
	Secondary Key
}

// EncryptionKey holds the key to encrypt an arbitrary object.
type Key struct {
	Key  string
	Name string
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
					Key:  string(m.Value.String()),
					Name: m.SecretKeyRef.Name,
				}

				continue
			}

			valid = true
		} else if m.Name == secondaryEncryptionKey {
			if len(m.Value.Raw) > 0 {
				cek.Secondary = Key{
					Key:  string(m.Value.String()),
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
			return ComponentEncryptionKeys{}, errors.Wrap(err, errPrefix)
		}

		if m.Name == primaryEncryptionKey {
			cek.Primary = key
		} else if m.Name == secondaryEncryptionKey {
			cek.Secondary = key
		}
	}

	return cek, nil
}

func tryGetEncryptionKeyFromMetadataItem(namespace string, item v1alpha1.MetadataItem, secretStore secretstores.SecretStore) (Key, error) {
	if item.SecretKeyRef.Name == "" {
		return Key{}, errors.Errorf("%s: secretKeyRef cannot be empty", errPrefix)
	}

	r, err := secretStore.GetSecret(secretstores.GetSecretRequest{
		Name: item.SecretKeyRef.Name,
		Metadata: map[string]string{
			"namespace": namespace,
		},
	})
	if err != nil {
		return Key{}, errors.Wrap(err, errPrefix)
	}

	key := item.SecretKeyRef.Key
	if key == "" {
		key = item.SecretKeyRef.Name
	}

	if val, ok := r.Data[key]; ok {
		if val == "" {
			return Key{}, errors.Errorf("%s: encryption key cannot be empty", errPrefix)
		}

		return Key{
			Key:  r.Data[key],
			Name: item.SecretKeyRef.Name,
		}, nil
	}

	return Key{}, nil
}

// Encrypt takes a byte array and encrypts it using a supplied encryption key and algorithm.
func encrypt(value []byte, key Key, algorithm Algorithm) ([]byte, error) {
	keyBytes, err := hex.DecodeString(key.Key)
	if err != nil {
		return value, err
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return value, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return value, err
	}

	nsize := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nsize); err != nil {
		return value, err
	}

	return gcm.Seal(nsize, nsize, value, nil), nil
}

// Decrypt takes a byte array and decrypts it using a supplied encryption key and algorithm.
func decrypt(value []byte, key Key, algorithm Algorithm) ([]byte, error) {
	keyBytes, err := hex.DecodeString(key.Key)
	if err != nil {
		return value, err
	}

	enc, err := hex.DecodeString(string(value))
	if err != nil {
		return value, err
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return value, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return value, err
	}

	nsize := gcm.NonceSize()
	nonce, ciphertext := enc[:nsize], enc[nsize:]

	return gcm.Open(nil, nonce, ciphertext, nil)
}
