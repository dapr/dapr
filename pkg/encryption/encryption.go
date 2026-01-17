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
	"github.com/dapr/kit/crypto/aescbcaead"
)

type Algorithm string

const (
	primaryEncryptionKey   = "primaryEncryptionKey"
	secondaryEncryptionKey = "secondaryEncryptionKey"
	errPrefix              = "failed to extract encryption key"
	AESCBCAEADAlgorithm    = "AES-CBC-AEAD"
	AESGCMAlgorithm        = "AES-GCM"
)

// ComponentEncryptionKeys holds the encryption keys set for a component.
type ComponentEncryptionKeys struct {
	Primary   Key
	Secondary Key
}

// Key holds the key to encrypt an arbitrary object.
type Key struct {
	Key         string
	Name        string
	cipherObj   cipher.AEAD // v1 AES-GCM encryption scheme
	cipherObjV2 cipher.AEAD // v2 AES-CBC-AEAD encryption scheme
}

type EncryptOpts struct {
	// TODO: remove when feature flag is removed
	StateStoreV2EncryptionEnabled bool
}

type DecryptOpts struct {
	Tag []byte // Authentication tag

	// TODO: remove when feature flag is removed
	StateStoreV2EncryptionEnabled bool
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

		cipherObjV2, err := createCipher(cek.Primary, AESCBCAEADAlgorithm)
		if err != nil {
			return ComponentEncryptionKeys{}, err
		}

		cek.Primary.cipherObj = cipherObj
		cek.Primary.cipherObjV2 = cipherObjV2
	}
	if cek.Secondary.Key != "" {
		cipherObj, err := createCipher(cek.Secondary, AESGCMAlgorithm)
		if err != nil {
			return ComponentEncryptionKeys{}, err
		}

		cipherObjV2, err := createCipher(cek.Secondary, AESCBCAEADAlgorithm)
		if err != nil {
			return ComponentEncryptionKeys{}, err
		}

		cek.Secondary.cipherObj = cipherObj
		cek.Secondary.cipherObjV2 = cipherObjV2
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
func encrypt(value []byte, key Key, additionalData []byte, opts EncryptOpts) ([]byte, error) {
	if opts.StateStoreV2EncryptionEnabled {

		nsize := make([]byte, key.cipherObjV2.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nsize); err != nil {
			return value, err
		}

		enc := key.cipherObjV2.Seal(nil, nsize, value, additionalData)

		// With v2 AES-CBC-AEAD the authentication tag is appended to the end, nonce is prepended manually
		return append(nsize, enc...), nil
	}

	nsize := make([]byte, key.cipherObj.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nsize); err != nil {
		return value, err
	}

	return key.cipherObj.Seal(nsize, nsize, value, nil), nil
}

// Decrypt takes a byte array and decrypts it using a supplied encryption key.
func decrypt(value []byte, key Key, additionalData []byte, opts DecryptOpts) ([]byte, error) {
	enc, err := b64.StdEncoding.DecodeString(string(value))
	if err != nil {
		return value, err
	}

	// TODO: once feature flag is removed the old scheme needs to be detected and handled
	if opts.StateStoreV2EncryptionEnabled {
		nsize := key.cipherObjV2.NonceSize()
		nonce, ciphertext := enc[:nsize], enc[nsize:]

		ciphertextWithTag := append(ciphertext, opts.Tag...)

		// NOTE: with v2 AES-CBC-AEAD the authentication tag must be appended to the end of the ciphertext
		return key.cipherObjV2.Open(nil, nonce, ciphertextWithTag, additionalData)
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
	case AESCBCAEADAlgorithm:
		return newAESCBCAEAD(keyBytes)
	case AESGCMAlgorithm:
		block, err := aes.NewCipher(keyBytes)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	}

	return nil, errors.New("unsupported algorithm")
}

// newAESCBCAEAD returns an AEAD cipher based on AES-CBC, or an error if the key size is not supported.
// Keys can be 32, 48, or 64 bytes, corresponding to AES-128 with HMAC-SHA-256-128, AES-192 with HMAC-SHA-384-192,
// or AES-256 with HMAC-SHA-512-256 respectively.
func newAESCBCAEAD(key []byte) (cipher.AEAD, error) {
	l := len(key)
	switch l {
	case 32:
		return aescbcaead.NewAESCBC128SHA256(key)
	case 48:
		return aescbcaead.NewAESCBC192SHA384(key)
	case 64:
		return aescbcaead.NewAESCBC256SHA512(key)
	default:
		return nil, errors.New("unsupported key size")
	}
}
