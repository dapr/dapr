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
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	b64 "encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

var encryptedStateStores = map[string]ComponentEncryptionKeys{}

const (
	separator = "||"
)

type EncryptedValue struct {
	// Version of the encryption scheme
	Version int `json:"v"`
	// Base64 ID of the encryption key as the SHA-224 hash of the key's bytes
	KeyID string `json:"kid"`
	// Ciphertext (IV is prepended)
	Ciphertext []byte `json:"cpt"`
	// Authentication tag
	Tag []byte `json:"tag"`
}

type TryEncryptValueOptions struct {
	// Additional data used to compute authentication tags
	KeyName string // The key that the value is mapped to in the store

	// TODO: remove when feature flag is removed
	StateV2EncryptionEnabled bool
}

type TryDecryptValueOptions struct {
	// Additional data used to compute authentication tags
	KeyName string // The key that the value is mapped to in the store

	// TODO: remove when feature flag is removed
	StateV2EncryptionEnabled bool
}

// AddEncryptedStateStore adds an encrypted state store and an associated encryption key to a list.
func AddEncryptedStateStore(storeName string, keys ComponentEncryptionKeys) bool {
	if _, ok := encryptedStateStores[storeName]; ok {
		return false
	}

	encryptedStateStores[storeName] = keys
	return true
}

// EncryptedStateStore returns a bool that indicates if a state stores supports encryption.
func EncryptedStateStore(storeName string) bool {
	_, ok := encryptedStateStores[storeName]
	return ok
}

// TryEncryptValue will try to encrypt a byte array if the state store has associated encryption keys.
// The function will append the name of the key to the value for later extraction.
// If no encryption keys exist, the function will return the bytes unmodified.
func TryEncryptValue(storeName string, value []byte, opts TryEncryptValueOptions) ([]byte, error) {
	keys := encryptedStateStores[storeName]

	if opts.StateV2EncryptionEnabled {
		// TODO: when feature flag is removed, replace old encryption logic with this
		// encrypted value is nonce || ciphertext || tag
		additionalData := []byte(opts.KeyName)
		enc, err := encrypt(value, keys.Primary, additionalData, EncryptOptions{
			StateV2EncryptionEnabled: opts.StateV2EncryptionEnabled,
		})
		if err != nil {
			return value, err
		}

		keyID := sha256.Sum224([]byte(keys.Primary.Key))
		tagSize := keys.Primary.cipherObj.Overhead()
		ciphertext, tag := enc[:len(enc)-tagSize], enc[len(enc)-tagSize:]
		encValue := EncryptedValue{
			Version:    2, // hardcoded version
			KeyID:      b64.StdEncoding.EncodeToString(keyID[:]),
			Ciphertext: ciphertext,
			Tag:        tag,
		}

		serializedEnc, err := json.Marshal(encValue)
		if err != nil {
			return value, err
		}

		return serializedEnc, nil
	}

	enc, err := encrypt(value, keys.Primary, nil, EncryptOptions{})
	if err != nil {
		return value, err
	}

	sEnc := b64.StdEncoding.EncodeToString(enc) + separator + keys.Primary.Name
	return []byte(sEnc), nil
}

// TryDecryptValue will try to decrypt a byte array if the state store has associated encryption keys.
// If no encryption keys exist, the function will return the bytes unmodified.
func TryDecryptValue(storeName string, value []byte, opts TryDecryptValueOptions) ([]byte, error) {
	if len(value) == 0 {
		return []byte(""), nil
	}

	keys := encryptedStateStores[storeName]

	// determine the encryption scheme
	encValue := &EncryptedValue{}
	err := json.Unmarshal(value, encValue)
	if err != nil {
		// fallback to old encryption scheme
		// extract the decryption key that should be appended to the value
		ind := bytes.LastIndex(value, []byte(separator))
		keyName := string(value[ind+len(separator):])

		if len(keyName) == 0 {
			return value, fmt.Errorf("could not decrypt data for state store %s: encryption key name not found on record", storeName)
		}

		var key Key

		if keys.Primary.Name == keyName {
			key = keys.Primary
		} else if keys.Secondary.Name == keyName {
			key = keys.Secondary
		}

		return decrypt(value[:ind], key, nil, DecryptOptions{})
	}

	if opts.StateV2EncryptionEnabled {
		// TODO: move to outer scope when feature flag is removed
		// determine which encryption key to use by comparing hashes
		var key Key

		if keyMatchesKeyID(keys.Primary, encValue.KeyID) {
			key = keys.Primary
		} else if keyMatchesKeyID(keys.Secondary, encValue.KeyID) {
			key = keys.Secondary
		}

		// with v2 AES-CBC-AEAD the authentication tag must be appended to the end of the ciphertext
		ciphertextWithTag := append(encValue.Ciphertext, encValue.Tag...)
		additionalData := []byte(opts.KeyName)

		return decrypt(ciphertextWithTag, key, additionalData, DecryptOptions{
			StateV2EncryptionEnabled: opts.StateV2EncryptionEnabled,
		})
	}

	// should never get here
	return nil, nil
}

// Returns a boolean indicating whether or not the key's SHA-224 hash is equivalent to the key ID.
// The key ID must be a Base-64 encoded string.
func keyMatchesKeyID(key Key, keyID string) bool {
	keyIDBytes, err := b64.StdEncoding.DecodeString(keyID)
	if err != nil {
		return false
	}

	keyBytes, err := hex.DecodeString(key.Key)
	if err != nil {
		return false
	}

	keyHashBytes := sha256.Sum224(keyBytes)

	if subtle.ConstantTimeCompare(keyHashBytes[:], keyIDBytes) == 1 {
		return true
	}

	return false
}
