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

package encryption

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"google.golang.org/protobuf/proto"

	contribcrypto "github.com/dapr/components-contrib/crypto"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	kitcrypto "github.com/dapr/kit/crypto"
)

const (
	selfCheckTimeout  = time.Minute
	selfCheckInterval = 250 * time.Millisecond

	// Unwrapped DEKs are cached so a remote KEK is called once per instance
	// per sidecar rather than on every load.
	cacheSize = 10_000
	cacheTTL  = 15 * time.Minute
)

// Options configures an Encryptor.
type Options struct {
	ComponentStore *compstore.ComponentStore
	Resiliency     resiliency.Provider

	// ComponentName is the crypto component holding the KEK.
	ComponentName string
	// KeyName is the KEK's name in the component, as "name" or
	// "name/version".
	KeyName string
	// WrapAlgorithm is the key wrapping algorithm. Empty means A256KW.
	WrapAlgorithm string
}

// Encryptor creates and opens per-instance keys through a crypto component.
type Encryptor struct {
	compStore  *compstore.ComponentStore
	resiliency resiliency.Provider
	component  string
	keyName    string
	alg        string
	cache      *expirable.LRU[string, []byte]
}

// New returns an Encryptor. It fails on an unsupported wrap algorithm.
// The crypto component is looked up on each use, because components are
// loaded after the workflow engine is built and can be hot reloaded.
func New(opts Options) (*Encryptor, error) {
	if opts.ComponentName == "" || opts.KeyName == "" {
		return nil, errors.New("crypto component and key name are required")
	}
	alg := opts.WrapAlgorithm
	if alg == "" {
		alg = kitcrypto.Algorithm_A256KW
	}
	switch alg {
	case kitcrypto.Algorithm_A256KW, kitcrypto.Algorithm_RSA_OAEP_256:
	default:
		return nil, fmt.Errorf("unsupported key wrap algorithm '%s': use %s or %s", alg, kitcrypto.Algorithm_A256KW, kitcrypto.Algorithm_RSA_OAEP_256)
	}
	return &Encryptor{
		compStore:  opts.ComponentStore,
		resiliency: opts.Resiliency,
		component:  opts.ComponentName,
		keyName:    opts.KeyName,
		alg:        alg,
		cache:      expirable.NewLRU[string, []byte](cacheSize, nil, cacheTTL),
	}, nil
}

// CurrentKID is the identifier new keys are wrapped under.
func (e *Encryptor) CurrentKID() string {
	return e.component + "/" + e.keyName
}

// NewInstanceKey generates a DEK and wraps it with the configured KEK.
func (e *Encryptor) NewInstanceKey(ctx context.Context) (*InstanceKey, error) {
	dek := make([]byte, dekSize)
	_, _ = rand.Read(dek)
	return e.wrapAndCache(ctx, dek)
}

// Rewrap wraps k's DEK with the configured KEK, for KEK rotation.
func (e *Encryptor) Rewrap(ctx context.Context, k *InstanceKey) (*InstanceKey, error) {
	return e.wrapAndCache(ctx, k.dek)
}

// Open unwraps a stored key. Unwrapped DEKs are cached, so a remote KEK is
// called once per key per sidecar.
func (e *Encryptor) Open(ctx context.Context, w *internalsv1pb.WorkflowEncryptionKey) (*InstanceKey, error) {
	ck := cacheKey(w)
	if dek, ok := e.cache.Get(ck); ok {
		return newInstanceKey(dek, w)
	}

	component, keyName, ok := strings.Cut(w.GetKid(), "/")
	if !ok || component == "" || keyName == "" {
		return nil, fmt.Errorf("malformed key id '%s'", w.GetKid())
	}
	dek, err := e.unwrap(ctx, component, keyName, w)
	if err != nil {
		return nil, err
	}
	k, err := newInstanceKey(dek, w)
	if err != nil {
		return nil, err
	}
	e.cache.Add(ck, dek)
	return k, nil
}

// OpenHeader opens a key encoded by InstanceKey.Header.
func (e *Encryptor) OpenHeader(ctx context.Context, header string) (*InstanceKey, error) {
	b, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return nil, fmt.Errorf("malformed encryption key header: %w", err)
	}
	var w internalsv1pb.WorkflowEncryptionKey
	if err := proto.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("malformed encryption key header: %w", err)
	}
	return e.Open(ctx, &w)
}

// SelfCheck waits for the crypto component to load, then wraps and unwraps a
// throwaway key with it. Workflows must not run on a sidecar that cannot
// encrypt.
func (e *Encryptor) SelfCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, selfCheckTimeout)
	defer cancel()
	for {
		if _, ok := e.compStore.GetCryptoProvider(e.component); ok {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("crypto component '%s' not found", e.component)
		case <-time.After(selfCheckInterval):
		}
	}

	dek := make([]byte, dekSize)
	_, _ = rand.Read(dek)
	w, err := e.wrap(ctx, dek)
	if err != nil {
		return err
	}
	got, err := e.unwrap(ctx, e.component, e.keyName, w)
	if err != nil {
		return err
	}
	if !bytes.Equal(dek, got) {
		return fmt.Errorf("crypto component '%s' key '%s' did not unwrap the key it wrapped", e.component, e.keyName)
	}
	return nil
}

func (e *Encryptor) wrapAndCache(ctx context.Context, dek []byte) (*InstanceKey, error) {
	w, err := e.wrap(ctx, dek)
	if err != nil {
		return nil, err
	}
	k, err := newInstanceKey(dek, w)
	if err != nil {
		return nil, err
	}
	e.cache.Add(cacheKey(w), dek)
	return k, nil
}

type wrapResult struct {
	wrapped []byte
	tag     []byte
}

func (e *Encryptor) wrap(ctx context.Context, dek []byte) (*internalsv1pb.WorkflowEncryptionKey, error) {
	comp, err := e.provider(e.component)
	if err != nil {
		return nil, err
	}
	key, err := jwk.FromRaw(dek)
	if err != nil {
		return nil, fmt.Errorf("failed to import data encryption key: %w", err)
	}

	runner := resiliency.NewRunner[wrapResult](ctx,
		e.resiliency.ComponentOutboundPolicy(e.component, resiliency.Crypto),
	)
	start := time.Now()
	res, err := runner(func(ctx context.Context) (r wrapResult, rErr error) {
		r.wrapped, r.tag, rErr = comp.WrapKey(ctx, key, e.alg, e.keyName, nil, nil)
		return r, rErr
	})
	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, e.component, diag.CryptoOp, err == nil, diag.ElapsedSince(start))
	if err != nil {
		return nil, fmt.Errorf("failed to wrap data encryption key with '%s': %w", e.CurrentKID(), err)
	}

	return &internalsv1pb.WorkflowEncryptionKey{
		Kid:     e.CurrentKID(),
		Alg:     e.alg,
		Wrapped: res.wrapped,
		Tag:     res.tag,
	}, nil
}

func (e *Encryptor) unwrap(ctx context.Context, component, keyName string, w *internalsv1pb.WorkflowEncryptionKey) ([]byte, error) {
	comp, err := e.provider(component)
	if err != nil {
		return nil, err
	}

	runner := resiliency.NewRunner[jwk.Key](ctx,
		e.resiliency.ComponentOutboundPolicy(component, resiliency.Crypto),
	)
	start := time.Now()
	key, err := runner(func(ctx context.Context) (jwk.Key, error) {
		return comp.UnwrapKey(ctx, w.GetWrapped(), w.GetAlg(), keyName, w.GetNonce(), w.GetTag(), nil)
	})
	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, component, diag.CryptoOp, err == nil, diag.ElapsedSince(start))
	if err != nil {
		return nil, fmt.Errorf("failed to unwrap data encryption key with '%s': %w", w.GetKid(), err)
	}

	var dek []byte
	if err := key.Raw(&dek); err != nil {
		return nil, fmt.Errorf("failed to extract data encryption key: %w", err)
	}
	return dek, nil
}

func (e *Encryptor) provider(name string) (contribcrypto.SubtleCrypto, error) {
	comp, ok := e.compStore.GetCryptoProvider(name)
	if !ok {
		return nil, fmt.Errorf("crypto component '%s' not found", name)
	}
	return comp, nil
}

func cacheKey(w *internalsv1pb.WorkflowEncryptionKey) string {
	return w.GetKid() + "\x00" + string(w.GetWrapped())
}
