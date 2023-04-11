//go:build subtlecrypto
// +build subtlecrypto

/*
Copyright 2022 The Dapr Authors
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

package universalapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	daprt "github.com/dapr/dapr/pkg/testing"
)

func TestSubtleGetKeyAlpha1(t *testing.T) {
	fakeAPI := &UniversalAPI{
		Logger:     testLogger,
		Resiliency: resiliency.New(nil),
		CryptoProviders: map[string]contribCrypto.SubtleCrypto{
			"myvault": &daprt.FakeSubtleCrypto{},
		},
	}

	t.Run("return key in PEM format", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "myvault",
			Name:          "good-key",
			Format:        runtimev1pb.SubtleGetKeyAlpha1Request_PEM,
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "good-key", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "d8e7975660f2a44223afddbe0d8fd05305758f69227494d23de4458b7a0e5b0b")
	})

	t.Run("return key in JSON format", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "myvault",
			Name:          "good-key",
			Format:        runtimev1pb.SubtleGetKeyAlpha1Request_JSON,
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "good-key", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "2643402c63512dbc20b041c920b5828389a2ff1437f1bd7b406df95e693db2b4")
	})

	t.Run("default to PEM format", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "myvault",
			Name:          "good-key",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "good-key", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "d8e7975660f2a44223afddbe0d8fd05305758f69227494d23de4458b7a0e5b0b")
	})

	t.Run("key not found", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "myvault",
			Name:          "not-found",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Empty(t, res.Name)
		assert.Empty(t, res.PublicKey)
	})

	t.Run("key has key ID", func(t *testing.T) {
		res, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "myvault",
			Name:          "with-name",
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "my-name-is-giovanni-giorgio", res.Name)
		compareSHA256Hash(t, []byte(res.PublicKey), "d8e7975660f2a44223afddbe0d8fd05305758f69227494d23de4458b7a0e5b0b")
	})

	t.Run("no provider configured", func(t *testing.T) {
		bak := fakeAPI.CryptoProviders
		fakeAPI.CryptoProviders = nil
		defer func() {
			fakeAPI.CryptoProviders = bak
		}()

		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("invalid format", func(t *testing.T) {
		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "myvault",
			Format:        runtimev1pb.SubtleGetKeyAlpha1Request_KeyFormat(-9000),
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrBadRequest)
		assert.ErrorContains(t, err, "invalid key format")
	})

	t.Run("failed to get key", func(t *testing.T) {
		_, err := fakeAPI.SubtleGetKeyAlpha1(context.Background(), &runtimev1pb.SubtleGetKeyAlpha1Request{
			ComponentName: "myvault",
			Name:          "error-key",
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrCryptoGetKey)
		assert.ErrorContains(t, err, "error occurs with error-key")
	})
}

func TestSubtleEncryptAlpha1(t *testing.T) {
	fakeAPI := &UniversalAPI{
		Logger:     testLogger,
		Resiliency: resiliency.New(nil),
		CryptoProviders: map[string]contribCrypto.SubtleCrypto{
			"myvault": &daprt.FakeSubtleCrypto{},
		},
	}

	t.Run("encrypt message", func(t *testing.T) {
		res, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptAlpha1Request{
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
		bak := fakeAPI.CryptoProviders
		fakeAPI.CryptoProviders = nil
		defer func() {
			fakeAPI.CryptoProviders = bak
		}()

		_, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptAlpha1Request{})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrCryptoProvidersNotConfigured)
	})

	t.Run("provider not found", func(t *testing.T) {
		_, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptAlpha1Request{
			ComponentName: "notfound",
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrCryptoProviderNotFound)
	})

	t.Run("failed to encrypt", func(t *testing.T) {
		_, err := fakeAPI.SubtleEncryptAlpha1(context.Background(), &runtimev1pb.SubtleEncryptAlpha1Request{
			ComponentName: "myvault",
			KeyName:       "error",
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrCryptoOperation)
		// The actual error is not returned to the user for security reasons
		assert.ErrorContains(t, err, "failed to encrypt")
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
