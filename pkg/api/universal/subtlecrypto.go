//go:build !subtlecrypto
// +build !subtlecrypto

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

	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// SubtleGetKeyAlpha1 returns the public part of an asymmetric key stored in the vault.
func (a *Universal) SubtleGetKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleGetKeyRequest) (*runtimev1pb.SubtleGetKeyResponse, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleEncryptAlpha1 encrypts a small message using a key stored in the vault.
func (a *Universal) SubtleEncryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleEncryptRequest) (*runtimev1pb.SubtleEncryptResponse, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleDecryptAlpha1 decrypts a small message using a key stored in the vault.
func (a *Universal) SubtleDecryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleDecryptRequest) (*runtimev1pb.SubtleDecryptResponse, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleWrapKeyAlpha1 wraps a key using a key stored in the vault.
func (a *Universal) SubtleWrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleWrapKeyRequest) (*runtimev1pb.SubtleWrapKeyResponse, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleUnwrapKeyAlpha1 unwraps a key using a key stored in the vault.
func (a *Universal) SubtleUnwrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleUnwrapKeyRequest) (*runtimev1pb.SubtleUnwrapKeyResponse, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleSignAlpha1 signs a message using a key stored in the vault.
func (a *Universal) SubtleSignAlpha1(ctx context.Context, in *runtimev1pb.SubtleSignRequest) (*runtimev1pb.SubtleSignResponse, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleVerifyAlpha1 verifies the signature of a message using a key stored in the vault.
func (a *Universal) SubtleVerifyAlpha1(ctx context.Context, in *runtimev1pb.SubtleVerifyRequest) (*runtimev1pb.SubtleVerifyResponse, error) {
	return nil, messages.ErrAPIUnimplemented
}

// CryptoValidateRequest is an internal method that checks if the request is for a valid crypto component.
func (a *Universal) CryptoValidateRequest(componentName string) (contribCrypto.SubtleCrypto, error) {
	if a.compStore.CryptoProvidersLen() == 0 {
		err := messages.ErrCryptoProvidersNotConfigured
		a.logger.Debug(err)
		return nil, err
	}

	if componentName == "" {
		err := messages.ErrBadRequest.WithFormat("missing component name")
		a.logger.Debug(err)
		return nil, err
	}

	component, ok := a.compStore.GetCryptoProvider(componentName)
	if !ok {
		err := messages.ErrCryptoProviderNotFound.WithFormat(componentName)
		a.logger.Debug(err)
		return nil, err
	}

	return component, nil
}
