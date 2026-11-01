/*
Copyright 2026 The Dapr Authors
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

// Package encryption seals the payloads of persisted workflow state with a
// per-instance data encryption key (DEK) that a crypto component's key
// encryption key (KEK) wraps.
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Prefix marks a sealed string value.
const Prefix = "enc:v1:"

const dekSize = 32

// ErrNotSealed is returned when a value that must be sealed is not.
var ErrNotSealed = errors.New("value is not sealed")

// InstanceKey is the unwrapped DEK of one workflow instance, together with
// its wrapped form.
type InstanceKey struct {
	dek     []byte
	aead    cipher.AEAD
	wrapped *internalsv1pb.WorkflowEncryptionKey
}

func newInstanceKey(dek []byte, wrapped *internalsv1pb.WorkflowEncryptionKey) (*InstanceKey, error) {
	if len(dek) != dekSize {
		return nil, fmt.Errorf("data encryption key must be %d bytes, got %d", dekSize, len(dek))
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &InstanceKey{dek: dek, aead: aead, wrapped: wrapped}, nil
}

// Seal encrypts plaintext with AES-256-GCM, binding it to aad. The result is
// nonce (12 bytes) || ciphertext || tag (16 bytes).
func (k *InstanceKey) Seal(aad string, plaintext []byte) []byte {
	return k.aead.Seal(nil, nil, plaintext, []byte(aad))
}

// Unseal reverses Seal. It fails if the value was sealed with another key or
// another aad, or was modified.
func (k *InstanceKey) Unseal(aad string, sealed []byte) ([]byte, error) {
	return k.aead.Open(nil, nil, sealed, []byte(aad))
}

// SealString seals s into a string carrying Prefix.
func (k *InstanceKey) SealString(aad, s string) string {
	return Prefix + base64.RawURLEncoding.EncodeToString(k.Seal(aad, []byte(s)))
}

// UnsealString reverses SealString. It returns ErrNotSealed when s does not
// carry Prefix.
func (k *InstanceKey) UnsealString(aad, s string) (string, error) {
	enc, ok := strings.CutPrefix(s, Prefix)
	if !ok {
		return "", ErrNotSealed
	}
	sealed, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("malformed sealed value: %w", err)
	}
	pt, err := k.Unseal(aad, sealed)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// Wrapped returns a copy of the wrapped form of the key.
func (k *InstanceKey) Wrapped() *internalsv1pb.WorkflowEncryptionKey {
	return proto.Clone(k.wrapped).(*internalsv1pb.WorkflowEncryptionKey)
}

// Header encodes the wrapped key for request metadata.
func (k *InstanceKey) Header() (string, error) {
	b, err := proto.Marshal(k.wrapped)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// NeedsRewrap reports whether the key was wrapped by a KEK other than kid.
func (k *InstanceKey) NeedsRewrap(kid string) bool {
	return k.wrapped.GetKid() != kid
}
