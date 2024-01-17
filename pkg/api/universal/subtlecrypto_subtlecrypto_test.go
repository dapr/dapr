//go:build subtlecrypto
// +build subtlecrypto

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

//nolint:protogetter
package universal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	daprt "github.com/dapr/dapr/pkg/testing"
)

var oneHundredTwentyEightBits = []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

func TestSubtleGetKeyAlpha1(t *testing.T) {
	fakeCryptoProvider := &daprt.FakeSubtleCrypto{}
	compStore := compstore.New()
	compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	t.Run("return key in PEM format", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "myvault",
			Name:          "good-key",
			Format:        runtimev1pb.SubtleGetKeyRequest_PEM,
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "good-key", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "d8e7975660f2a44223afddbe0d8fd05305758f69227494d23de4458b7a0e5b0b")
	})

	t.Run("return key in JSON format", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "myvault",
			Name:          "good-key",
			Format:        runtimev1pb.SubtleGetKeyRequest_JSON,
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "good-key", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "2643402c63512dbc20b041c920b5828389a2ff1437f1bd7b406df95e693db2b4")
	})

	t.Run("default to PEM format", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "myvault",
			Name:          "good-key",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "good-key", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "d8e7975660f2a44223afddbe0d8fd05305758f69227494d23de4458b7a0e5b0b")
	})

	t.Run("key not found", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "myvault",
			Name:          "not-found",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Empty(t, res.Name)
		assert.Empty(t, res.PublicKey)
	})

	t.Run("key has key ID", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "myvault",
			Name:          "with-name",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "my-name-is-giovanni-giorgio", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "d8e7975660f2a44223afddbe0d8fd05305758f69227494d23de4458b7a0e5b0b")
	})

	t.Run("no provider configured", func(t *testing.T) {
		fakeAPI.compStore.DeleteCryptoProvider("myvault")
		defer func() {
			compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
		}()

		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("invalid format", func(t *testing.T) {
		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "myvault",
			Format:        runtimev1pb.SubtleGetKeyRequest_KeyFormat(-9000),
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrBadRequest)
		require.ErrorContains(t, err, "invalid key format")
	})

	t.Run("failed to get key", func(t *testing.T) {
		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyRequest{
			ComponentName: "myvault",
			Name:          "error-key",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoGetKey)
		require.ErrorContains(t, err, "error occurs with error-key")
	})
}

func TestSubtleEncryptAlpha1(t *testing.T) {
	fakeCryptoProvider := &daprt.FakeSubtleCrypto{}
	compStore := compstore.New()
	compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	t.Run("encrypt message", func(t *testing.T) {
		res, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptRequest{
			ComponentName: "myvault",
			Plaintext:     []byte("hello world"),
			KeyName:       "good-tag",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		// Message isn't actually encrypted
		assert.Equal(t, "hello world", string(res.Ciphertext))
		assert.Len(t, res.Tag, 16)
	})

	t.Run("no provider configured", func(t *testing.T) {
		fakeAPI.compStore.DeleteCryptoProvider("myvault")
		defer func() {
			compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
		}()

		_, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptRequest{})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptRequest{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("failed to encrypt", func(t *testing.T) {
		_, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptRequest{
			ComponentName: "myvault",
			KeyName:       "error",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoOperation)
		// The actual error is not returned to the user for security reasons
		require.ErrorContains(t, err, "failed to encrypt")
	})
}

func TestSubtleDecryptAlpha1(t *testing.T) {
	fakeCryptoProvider := &daprt.FakeSubtleCrypto{}
	compStore := compstore.New()
	compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	t.Run("decrypt message", func(t *testing.T) {
		res, err := fakeAPI.SubtleDecryptAlpha1(context.Background(), &runtimev1pb.SubtleDecryptRequest{
			ComponentName: "myvault",
			Ciphertext:    []byte("hello world"),
			KeyName:       "good",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		// Message isn't actually encrypted
		assert.Equal(t, "hello world", string(res.Plaintext))
	})

	t.Run("no provider configured", func(t *testing.T) {
		fakeAPI.compStore.DeleteCryptoProvider("myvault")
		defer func() {
			compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
		}()

		_, err := fakeAPI.SubtleDecryptAlpha1(context.Background(), &runtimev1pb.SubtleDecryptRequest{})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleDecryptAlpha1(context.Background(), &runtimev1pb.SubtleDecryptRequest{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("failed to decrypt", func(t *testing.T) {
		_, err := fakeAPI.SubtleDecryptAlpha1(context.Background(), &runtimev1pb.SubtleDecryptRequest{
			ComponentName: "myvault",
			KeyName:       "error",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoOperation)
		// The actual error is not returned to the user for security reasons
		require.ErrorContains(t, err, "failed to decrypt")
	})
}

func TestSubtleWrapKeyAlpha1(t *testing.T) {
	fakeCryptoProvider := &daprt.FakeSubtleCrypto{}
	compStore := compstore.New()
	compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	t.Run("wrap key", func(t *testing.T) {
		res, err := fakeAPI.SubtleWrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleWrapKeyRequest{
			ComponentName: "myvault",
			PlaintextKey:  []byte("hello world"),
			KeyName:       "good-tag",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, oneHundredTwentyEightBits, res.WrappedKey)
		assert.Len(t, res.Tag, 16)
	})

	t.Run("no provider configured", func(t *testing.T) {
		fakeAPI.compStore.DeleteCryptoProvider("myvault")
		defer func() {
			compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
		}()

		_, err := fakeAPI.SubtleWrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleWrapKeyRequest{})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleWrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleWrapKeyRequest{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("key is empty", func(t *testing.T) {
		_, err := fakeAPI.SubtleWrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleWrapKeyRequest{
			ComponentName: "myvault",
			KeyName:       "error",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoOperation)
		require.ErrorContains(t, err, "key is empty")
	})

	t.Run("failed to wrap key", func(t *testing.T) {
		_, err := fakeAPI.SubtleWrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleWrapKeyRequest{
			ComponentName: "myvault",
			KeyName:       "error",
			PlaintextKey:  oneHundredTwentyEightBits,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoOperation)
		// The actual error is not returned to the user for security reasons
		require.ErrorContains(t, err, "failed to wrap key")
	})
}

func TestSubtleUnwrapKeyAlpha1(t *testing.T) {
	fakeCryptoProvider := &daprt.FakeSubtleCrypto{}
	compStore := compstore.New()
	compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	t.Run("unwrap key", func(t *testing.T) {
		res, err := fakeAPI.SubtleUnwrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleUnwrapKeyRequest{
			ComponentName: "myvault",
			WrappedKey:    []byte("hello world"),
			KeyName:       "good",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		// Message isn't actually encrypted
		assert.Equal(t, oneHundredTwentyEightBits, res.PlaintextKey)
	})

	t.Run("no provider configured", func(t *testing.T) {
		fakeAPI.compStore.DeleteCryptoProvider("myvault")
		defer func() {
			compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
		}()

		_, err := fakeAPI.SubtleUnwrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleUnwrapKeyRequest{})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleUnwrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleUnwrapKeyRequest{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("failed to unwrap key", func(t *testing.T) {
		_, err := fakeAPI.SubtleUnwrapKeyAlpha1(context.Background(), &runtimev1pb.SubtleUnwrapKeyRequest{
			ComponentName: "myvault",
			KeyName:       "error",
			WrappedKey:    oneHundredTwentyEightBits,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoOperation)
		// The actual error is not returned to the user for security reasons
		require.ErrorContains(t, err, "failed to unwrap key")
	})
}

func TestSubtleSignAlpha1(t *testing.T) {
	fakeCryptoProvider := &daprt.FakeSubtleCrypto{}
	compStore := compstore.New()
	compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	t.Run("sign message", func(t *testing.T) {
		res, err := fakeAPI.SubtleSignAlpha1(context.Background(), &runtimev1pb.SubtleSignRequest{
			ComponentName: "myvault",
			Digest:        []byte("hello world"),
			KeyName:       "good",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		// Message isn't actually signed
		assert.Equal(t, oneHundredTwentyEightBits, res.Signature)
	})

	t.Run("no provider configured", func(t *testing.T) {
		fakeAPI.compStore.DeleteCryptoProvider("myvault")
		defer func() {
			compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
		}()

		_, err := fakeAPI.SubtleSignAlpha1(context.Background(), &runtimev1pb.SubtleSignRequest{})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleSignAlpha1(context.Background(), &runtimev1pb.SubtleSignRequest{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("failed to sign", func(t *testing.T) {
		_, err := fakeAPI.SubtleSignAlpha1(context.Background(), &runtimev1pb.SubtleSignRequest{
			ComponentName: "myvault",
			KeyName:       "error",
			Digest:        oneHundredTwentyEightBits,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoOperation)
		// The actual error is not returned to the user for security reasons
		require.ErrorContains(t, err, "failed to sign")
	})
}

func TestSubtleVerifyAlpha1(t *testing.T) {
	fakeCryptoProvider := &daprt.FakeSubtleCrypto{}
	compStore := compstore.New()
	compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	t.Run("signature is valid", func(t *testing.T) {
		res, err := fakeAPI.SubtleVerifyAlpha1(context.Background(), &runtimev1pb.SubtleVerifyRequest{
			ComponentName: "myvault",
			Digest:        oneHundredTwentyEightBits,
			Signature:     oneHundredTwentyEightBits,
			KeyName:       "good",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Valid)
	})

	t.Run("signature is invalid", func(t *testing.T) {
		res, err := fakeAPI.SubtleVerifyAlpha1(context.Background(), &runtimev1pb.SubtleVerifyRequest{
			ComponentName: "myvault",
			Digest:        oneHundredTwentyEightBits,
			Signature:     oneHundredTwentyEightBits,
			KeyName:       "bad",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.False(t, res.Valid)
	})

	t.Run("no provider configured", func(t *testing.T) {
		fakeAPI.compStore.DeleteCryptoProvider("myvault")
		defer func() {
			compStore.AddCryptoProvider("myvault", fakeCryptoProvider)
		}()

		_, err := fakeAPI.SubtleVerifyAlpha1(context.Background(), &runtimev1pb.SubtleVerifyRequest{})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleVerifyAlpha1(context.Background(), &runtimev1pb.SubtleVerifyRequest{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("failed to verify", func(t *testing.T) {
		_, err := fakeAPI.SubtleVerifyAlpha1(context.Background(), &runtimev1pb.SubtleVerifyRequest{
			ComponentName: "myvault",
			KeyName:       "error",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, messages.ErrCryptoOperation)
		// The actual error is not returned to the user for security reasons
		require.ErrorContains(t, err, "failed to verify")
	})
}

func compareSHA256Hash(t *testing.T, data []byte, expect string) {
	t.Helper()

	h := sha256.Sum256(data)
	actual := hex.EncodeToString(h[:])
	if actual != expect {
		t.Errorf("expected data to have hash %s, but got %s", expect, actual)
	}
}
