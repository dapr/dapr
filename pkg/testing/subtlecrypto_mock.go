/*
Copyright 2023 The Dapr Authors
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

//nolint:goconst
package testing

import (
	"context"
	"errors"

	// Blank import for the embed package.
	_ "embed"

	"github.com/lestrrat-go/jwx/v2/jwk"

	contribCrypto "github.com/dapr/components-contrib/crypto"
)

var (
	//go:embed rsa-pub.json
	rsaPubJWK                 []byte
	oneHundredTwentyEightBits = []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
)

// Type assertions for the interface
var (
	_ contribCrypto.SubtleCrypto = (*FakeSubtleCrypto)(nil)
	_ contribCrypto.SubtleCrypto = (*FailingSubtleCrypto)(nil)
)

type FakeSubtleCrypto struct{}

func (c FakeSubtleCrypto) GetKey(ctx context.Context, keyName string) (pubKey jwk.Key, err error) {
	switch keyName {
	case "good-key":
		return jwk.ParseKey(rsaPubJWK)
	case "error-key":
		return nil, errors.New("error occurs with error-key")
	case "with-name":
		pubKey, _ = jwk.ParseKey(rsaPubJWK)
		pubKey = contribCrypto.NewKey(pubKey, "my-name-is-giovanni-giorgio", nil, nil)
		return
	}

	return nil, nil
}

func (c FakeSubtleCrypto) Encrypt(ctx context.Context, plaintext []byte, algorithm string, keyName string, nonce []byte, associatedData []byte) (ciphertext []byte, tag []byte, err error) {
	// Doesn't actually do any encryption
	switch keyName {
	case "good-notag":
		ciphertext = plaintext
	case "good-tag":
		ciphertext = plaintext
		tag = oneHundredTwentyEightBits
	case "error":
		err = errors.New("simulated error")
	}
	return
}

func (c FakeSubtleCrypto) Decrypt(ctx context.Context, ciphertext []byte, algorithm string, keyName string, nonce []byte, tag []byte, associatedData []byte) (plaintext []byte, err error) {
	// Doesn't actually do any encryption
	switch keyName {
	case "good":
		plaintext = ciphertext
	case "error":
		err = errors.New("simulated error")
	}
	return
}

func (c FakeSubtleCrypto) WrapKey(ctx context.Context, plaintextKey jwk.Key, algorithm string, keyName string, nonce []byte, associatedData []byte) (wrappedKey []byte, tag []byte, err error) {
	// Doesn't actually do any encryption
	switch keyName {
	case "good-notag":
		wrappedKey = oneHundredTwentyEightBits
	case "good-tag":
		wrappedKey = oneHundredTwentyEightBits
		tag = oneHundredTwentyEightBits
	case "error":
		err = errors.New("simulated error")
	case "aes-passthrough":
		err = plaintextKey.Raw(&wrappedKey)
	}
	return
}

func (c FakeSubtleCrypto) UnwrapKey(ctx context.Context, wrappedKey []byte, algorithm string, keyName string, nonce []byte, tag []byte, associatedData []byte) (plaintextKey jwk.Key, err error) {
	// Doesn't actually do any encryption
	switch keyName {
	case "good", "good-notag", "good-tag":
		return jwk.FromRaw(oneHundredTwentyEightBits)
	case "error":
		err = errors.New("simulated error")
	case "aes-passthrough":
		return jwk.FromRaw(wrappedKey)
	}
	return
}

func (c FakeSubtleCrypto) Sign(ctx context.Context, digest []byte, algorithm string, keyName string) (signature []byte, err error) {
	switch keyName {
	case "good":
		signature = oneHundredTwentyEightBits
	case "error":
		err = errors.New("simulated error")
	}
	return
}

func (c FakeSubtleCrypto) Verify(ctx context.Context, digest []byte, signature []byte, algorithm string, keyName string) (valid bool, err error) {
	switch keyName {
	case "good":
		valid = true
	case "bad":
		valid = false
	case "error":
		err = errors.New("simulated error")
	}
	return
}

func (c FakeSubtleCrypto) SupportedEncryptionAlgorithms() []string {
	return []string{}
}

func (c FakeSubtleCrypto) SupportedSignatureAlgorithms() []string {
	return []string{}
}

func (c FakeSubtleCrypto) Init(ctx context.Context, metadata contribCrypto.Metadata) error {
	return nil
}

func (c FakeSubtleCrypto) Close() error {
	return nil
}

func (c FakeSubtleCrypto) Features() []contribCrypto.Feature {
	return []contribCrypto.Feature{}
}

type FailingSubtleCrypto struct {
	FakeSubtleCrypto
	Failure Failure
}

func (c FailingSubtleCrypto) GetKey(ctx context.Context, keyName string) (pubKey jwk.Key, err error) {
	err = c.Failure.PerformFailure(keyName)
	if err != nil {
		return
	}

	return c.FakeSubtleCrypto.GetKey(ctx, keyName)
}

func (c FailingSubtleCrypto) Encrypt(ctx context.Context, plaintext []byte, algorithm string, keyName string, nonce []byte, associatedData []byte) (ciphertext []byte, tag []byte, err error) {
	err = c.Failure.PerformFailure(algorithm)
	if err != nil {
		return
	}

	return c.FakeSubtleCrypto.Encrypt(ctx, plaintext, algorithm, keyName, nonce, associatedData)
}

func (c FailingSubtleCrypto) Decrypt(ctx context.Context, ciphertext []byte, algorithm string, keyName string, nonce []byte, tag []byte, associatedData []byte) (plaintext []byte, err error) {
	err = c.Failure.PerformFailure(algorithm)
	if err != nil {
		return
	}

	return c.FakeSubtleCrypto.Decrypt(ctx, ciphertext, algorithm, keyName, nonce, tag, associatedData)
}

func (c FailingSubtleCrypto) WrapKey(ctx context.Context, plaintextKey jwk.Key, algorithm string, keyName string, nonce []byte, associatedData []byte) (wrappedKey []byte, tag []byte, err error) {
	err = c.Failure.PerformFailure(algorithm)
	if err != nil {
		return
	}

	return c.FakeSubtleCrypto.WrapKey(ctx, plaintextKey, algorithm, keyName, nonce, associatedData)
}

func (c FailingSubtleCrypto) UnwrapKey(ctx context.Context, wrappedKey []byte, algorithm string, keyName string, nonce []byte, tag []byte, associatedData []byte) (plaintextKey jwk.Key, err error) {
	err = c.Failure.PerformFailure(algorithm)
	if err != nil {
		return
	}

	return c.FakeSubtleCrypto.UnwrapKey(ctx, wrappedKey, algorithm, keyName, nonce, tag, associatedData)
}

func (c FailingSubtleCrypto) Sign(ctx context.Context, digest []byte, algorithm string, keyName string) (signature []byte, err error) {
	err = c.Failure.PerformFailure(algorithm)
	if err != nil {
		return
	}

	return c.FakeSubtleCrypto.Sign(ctx, digest, algorithm, keyName)
}

func (c FailingSubtleCrypto) Verify(ctx context.Context, digest []byte, signature []byte, algorithm string, keyName string) (valid bool, err error) {
	err = c.Failure.PerformFailure(algorithm)
	if err != nil {
		return
	}

	return c.FakeSubtleCrypto.Verify(ctx, digest, signature, algorithm, keyName)
}
