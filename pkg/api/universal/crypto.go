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

package universal

import (
	"context"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	encv1 "github.com/dapr/kit/schemes/enc/v1"
)

type subtleWrapKeyRes struct {
	wrappedKey []byte
	tag        []byte
}

func (a *Universal) CryptoGetWrapKeyFn(ctx context.Context, componentName string, component contribCrypto.SubtleCrypto) encv1.WrapKeyFn {
	return func(plaintextKeyBytes []byte, algorithm, keyName string, nonce []byte) (wrappedKey []byte, tag []byte, err error) {
		plaintextKey, err := jwk.FromRaw(plaintextKeyBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to import key: %w", err)
		}

		policyRunner := resiliency.NewRunner[subtleWrapKeyRes](ctx,
			a.resiliency.ComponentOutboundPolicy(componentName, resiliency.Crypto),
		)
		start := time.Now()
		swkr, err := policyRunner(func(ctx context.Context) (r subtleWrapKeyRes, rErr error) {
			r.wrappedKey, r.tag, rErr = component.WrapKey(ctx, plaintextKey, algorithm, keyName, nonce, nil)
			return
		})
		elapsed := diag.ElapsedSince(start)

		diag.DefaultComponentMonitoring.CryptoInvoked(ctx, componentName, diag.CryptoOp, err == nil, elapsed)

		if err != nil {
			return nil, nil, err
		}
		return swkr.wrappedKey, swkr.tag, nil
	}
}

func (a *Universal) CryptoGetUnwrapKeyFn(ctx context.Context, componentName string, component contribCrypto.SubtleCrypto) encv1.UnwrapKeyFn {
	return func(wrappedKey []byte, algorithm, keyName string, nonce, tag []byte) (plaintextKeyBytes []byte, err error) {
		policyRunner := resiliency.NewRunner[jwk.Key](ctx,
			a.resiliency.ComponentOutboundPolicy(componentName, resiliency.Crypto),
		)
		start := time.Now()
		plaintextKey, err := policyRunner(func(ctx context.Context) (jwk.Key, error) {
			return component.UnwrapKey(ctx, wrappedKey, algorithm, keyName, nonce, tag, nil)
		})
		elapsed := diag.ElapsedSince(start)

		diag.DefaultComponentMonitoring.CryptoInvoked(ctx, componentName, diag.CryptoOp, err == nil, elapsed)

		if err != nil {
			return nil, err
		}

		err = plaintextKey.Raw(&plaintextKeyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to extract key: %w", err)
		}

		return plaintextKeyBytes, nil
	}
}
