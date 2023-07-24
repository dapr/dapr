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
	b64 "encoding/base64"
	"fmt"
)

var encryptedStateStores = map[string]ComponentEncryptionKeys{}

const (
	separator = "||"
)

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
func TryEncryptValue(storeName string, value []byte) ([]byte, error) {
	keys := encryptedStateStores[storeName]
	enc, err := encrypt(value, keys.Primary)
	if err != nil {
		return value, err
	}

	sEnc := b64.StdEncoding.EncodeToString(enc) + separator + keys.Primary.Name
	return []byte(sEnc), nil
}

// TryDecryptValue will try to decrypt a byte array if the state store has associated encryption keys.
// If no encryption keys exist, the function will return the bytes unmodified.
func TryDecryptValue(storeName string, value []byte) ([]byte, error) {
	if len(value) == 0 {
		return []byte(""), nil
	}

	keys := encryptedStateStores[storeName]
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

	return decrypt(value[:ind], key)
}
