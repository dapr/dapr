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

package universalapi

import (
	"context"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// SubtleGetKeyAlpha1 returns the public part of an asymmetric key stored in the vault.
func (a *UniversalAPI) SubtleGetKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleGetKeyAlpha1Request) (*runtimev1pb.SubtleGetKeyAlpha1Response, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleEncryptAlpha1 encrypts a small message using a key stored in the vault.
func (a *UniversalAPI) SubtleEncryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleEncryptAlpha1Request) (*runtimev1pb.SubtleEncryptAlpha1Response, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleDecryptAlpha1 decrypts a small message using a key stored in the vault.
func (a *UniversalAPI) SubtleDecryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleDecryptAlpha1Request) (*runtimev1pb.SubtleDecryptAlpha1Response, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleWrapKeyAlpha1 wraps a key using a key stored in the vault.
func (a *UniversalAPI) SubtleWrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleWrapKeyAlpha1Request) (*runtimev1pb.SubtleWrapKeyAlpha1Response, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleUnwrapKeyAlpha1 unwraps a key using a key stored in the vault.
func (a *UniversalAPI) SubtleUnwrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleUnwrapKeyAlpha1Request) (*runtimev1pb.SubtleUnwrapKeyAlpha1Response, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleSignAlpha1 signs a message using a key stored in the vault.
func (a *UniversalAPI) SubtleSignAlpha1(ctx context.Context, in *runtimev1pb.SubtleSignAlpha1Request) (*runtimev1pb.SubtleSignAlpha1Response, error) {
	return nil, messages.ErrAPIUnimplemented
}

// SubtleVerifyAlpha1 verifies the signature of a message using a key stored in the vault.
func (a *UniversalAPI) SubtleVerifyAlpha1(ctx context.Context, in *runtimev1pb.SubtleVerifyAlpha1Request) (*runtimev1pb.SubtleVerifyAlpha1Response, error) {
	return nil, messages.ErrAPIUnimplemented
}

// CryptoValidateRequest is an internal method that checks if the request is for a valid crypto component.
func (a *UniversalAPI) CryptoValidateRequest(componentName string) (contribCrypto.SubtleCrypto, error) {
	if a.CompStore.CryptoProvidersLen() == 0 {
		err := messages.ErrCryptoProvidersNotConfigured
		a.Logger.Debug(err)
		return nil, err
	}

	if componentName == "" {
		err := messages.ErrBadRequest.WithFormat("missing component name")
		a.Logger.Debug(err)
		return nil, err
	}

	component, ok := a.CompStore.GetCryptoProvider(componentName)
	if !ok {
		err := messages.ErrCryptoProviderNotFound.WithFormat(componentName)
		a.Logger.Debug(err)
		return nil, err
	}

	return component, nil
}
