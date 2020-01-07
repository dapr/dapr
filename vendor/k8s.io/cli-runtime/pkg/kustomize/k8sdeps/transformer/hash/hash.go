/*
Copyright 2017 The Kubernetes Authors.

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

package hash

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// KustHash compute hash for unstructured objects
type KustHash struct{}

// NewKustHash returns a KustHash object
func NewKustHash() *KustHash {
	return &KustHash{}
}

// Hash returns a hash of either a ConfigMap or a Secret
func (h *KustHash) Hash(m map[string]interface{}) (string, error) {
	u := unstructured.Unstructured{
		Object: m,
	}
	kind := u.GetKind()
	switch kind {
	case "ConfigMap":
		cm, err := unstructuredToConfigmap(u)
		if err != nil {
			return "", err
		}
		return ConfigMapHash(cm)
	case "Secret":
		sec, err := unstructuredToSecret(u)

		if err != nil {
			return "", err
		}
		return SecretHash(sec)
	default:
		return "", fmt.Errorf("type %s is supported for hashing in %v", kind, m)
	}
}

// ConfigMapHash returns a hash of the ConfigMap.
// The Data, Kind, and Name are taken into account.
func ConfigMapHash(cm *v1.ConfigMap) (string, error) {
	encoded, err := encodeConfigMap(cm)
	if err != nil {
		return "", err
	}
	h, err := encodeHash(hash(encoded))
	if err != nil {
		return "", err
	}
	return h, nil
}

// SecretHash returns a hash of the Secret.
// The Data, Kind, Name, and Type are taken into account.
func SecretHash(sec *v1.Secret) (string, error) {
	encoded, err := encodeSecret(sec)
	if err != nil {
		return "", err
	}
	h, err := encodeHash(hash(encoded))
	if err != nil {
		return "", err
	}
	return h, nil
}

// encodeConfigMap encodes a ConfigMap.
// Data, Kind, and Name are taken into account.
func encodeConfigMap(cm *v1.ConfigMap) (string, error) {
	// json.Marshal sorts the keys in a stable order in the encoding
	m := map[string]interface{}{"kind": "ConfigMap", "name": cm.Name, "data": cm.Data}
	if len(cm.BinaryData) > 0 {
		m["binaryData"] = cm.BinaryData
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// encodeSecret encodes a Secret.
// Data, Kind, Name, and Type are taken into account.
func encodeSecret(sec *v1.Secret) (string, error) {
	// json.Marshal sorts the keys in a stable order in the encoding
	data, err := json.Marshal(map[string]interface{}{"kind": "Secret", "type": sec.Type, "name": sec.Name, "data": sec.Data})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// encodeHash extracts the first 40 bits of the hash from the hex string
// (1 hex char represents 4 bits), and then maps vowels and vowel-like hex
// characters to consonants to prevent bad words from being formed (the theory
// is that no vowels makes it really hard to make bad words). Since the string
// is hex, the only vowels it can contain are 'a' and 'e'.
// We picked some arbitrary consonants to map to from the same character set as GenerateName.
// See: https://github.com/kubernetes/apimachinery/blob/dc1f89aff9a7509782bde3b68824c8043a3e58cc/pkg/util/rand/rand.go#L75
// If the hex string contains fewer than ten characters, returns an error.
func encodeHash(hex string) (string, error) {
	if len(hex) < 10 {
		return "", fmt.Errorf("the hex string must contain at least 10 characters")
	}
	enc := []rune(hex[:10])
	for i := range enc {
		switch enc[i] {
		case '0':
			enc[i] = 'g'
		case '1':
			enc[i] = 'h'
		case '3':
			enc[i] = 'k'
		case 'a':
			enc[i] = 'm'
		case 'e':
			enc[i] = 't'
		}
	}
	return string(enc), nil
}

// hash hashes `data` with sha256 and returns the hex string
func hash(data string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}

func unstructuredToConfigmap(u unstructured.Unstructured) (*v1.ConfigMap, error) {
	marshaled, err := json.Marshal(u.Object)
	if err != nil {
		return nil, err
	}
	var out v1.ConfigMap
	err = json.Unmarshal(marshaled, &out)
	return &out, err
}

func unstructuredToSecret(u unstructured.Unstructured) (*v1.Secret, error) {
	marshaled, err := json.Marshal(u.Object)
	if err != nil {
		return nil, err
	}
	var out v1.Secret
	err = json.Unmarshal(marshaled, &out)
	return &out, err
}
