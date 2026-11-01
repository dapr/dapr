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

// Package encryptiontest provides workflow payload encryptors backed by a
// local storage crypto component, for tests.
package encryptiontest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	contribcrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/components-contrib/crypto/localstorage"
	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption"
	"github.com/dapr/kit/logger"
)

const (
	// Component is the name of the crypto component holding the KEKs.
	Component = "kek-store"
	// Key is the KEK every Env starts with.
	Key = "kek.json"
)

// Env is a component store with a local storage crypto component whose keys
// are files in a temporary directory.
type Env struct {
	Store *compstore.ComponentStore
	dir   string
}

// NewEnv returns an Env holding one random 256-bit KEK named Key.
func NewEnv(t testing.TB) *Env {
	t.Helper()
	e := &Env{Store: compstore.New(), dir: t.TempDir()}
	e.AddKey(t, Key)

	comp := localstorage.NewLocalStorageCrypto(logger.NewLogger("test"))
	require.NoError(t, comp.Init(t.Context(), contribcrypto.Metadata{Base: metadata.Base{
		Properties: map[string]string{"path": e.dir},
	}}))
	e.Store.AddCryptoProvider(Component, comp)
	return e
}

// AddKey writes another random 256-bit KEK named name.
func (e *Env) AddKey(t testing.TB, name string) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	jwk := `{"kty":"oct","k":"` + base64.RawURLEncoding.EncodeToString(k) + `"}`
	require.NoError(t, os.WriteFile(filepath.Join(e.dir, name), []byte(jwk), 0o600))
}

// Encryptor returns an Encryptor wrapping new keys with keyName.
func (e *Env) Encryptor(t testing.TB, keyName string) *encryption.Encryptor {
	t.Helper()
	enc, err := encryption.New(encryption.Options{
		ComponentStore: e.Store,
		Resiliency:     resiliency.New(logger.NewLogger("test")),
		ComponentName:  Component,
		KeyName:        keyName,
	})
	require.NoError(t, err)
	return enc
}

// NewEncryptor returns an Encryptor with a fresh KEK.
func NewEncryptor(t testing.TB) *encryption.Encryptor {
	t.Helper()
	return NewEnv(t).Encryptor(t, Key)
}

// NewKey returns a fresh instance key.
func NewKey(t testing.TB) *encryption.InstanceKey {
	t.Helper()
	k, err := NewEncryptor(t).NewInstanceKey(context.Background())
	require.NoError(t, err)
	return k
}
