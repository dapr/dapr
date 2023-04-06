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

package universalapi

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	kitCrypto "github.com/dapr/kit/crypto"
)

// SubtleGetKeyAlpha1 returns the public part of an asymmetric key stored in the vault.
func (a *UniversalAPI) SubtleGetKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleGetKeyAlpha1Request) (*runtimev1pb.SubtleGetKeyAlpha1Response, error) {
	component, err := a.CryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
	}
	switch in.Format {
	case runtimev1pb.SubtleGetKeyAlpha1Request_PEM,
		runtimev1pb.SubtleGetKeyAlpha1Request_JSON: //nolint:nosnakecase
		// Nop
	default:
		err = messages.ErrBadRequest.WithFormat("invalid key format")
		a.Logger.Debug(err)
		return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
	}

	// Get the key
	policyRunner := resiliency.NewRunner[jwk.Key](ctx,
		a.Resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	res, err := policyRunner(func(ctx context.Context) (jwk.Key, error) {
		return component.GetKey(ctx, in.Name)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = messages.ErrCryptoGetKey.WithFormat(in.Name, err)
		a.Logger.Debug(err)
		return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
	}

	// Get the key ID if present
	kid := in.Name
	if dk, ok := res.(*contribCrypto.Key); ok {
		kid = dk.KeyID()
	}

	// Format the response
	var pk []byte
	switch in.Format {
	case runtimev1pb.SubtleGetKeyAlpha1Request_PEM: //nolint:nosnakecase
		var (
			v   crypto.PublicKey
			der []byte
		)
		err = res.Raw(&v)
		if err != nil {
			err = fmt.Errorf("failed to marshal public key %s as PKIX: %w", in.Name, err)
			err = messages.ErrCryptoGetKey.WithFormat(in.Name, err)
			a.Logger.Debug(err)
			return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
		}
		der, err = x509.MarshalPKIXPublicKey(v)
		if err != nil {
			err = fmt.Errorf("failed to marshal public key %s as PKIX: %w", in.Name, err)
			err = messages.ErrCryptoGetKey.WithFormat(in.Name, err)
			a.Logger.Debug(err)
			return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
		}
		pk = pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: der,
		})

	case runtimev1pb.SubtleGetKeyAlpha1Request_JSON: //nolint:nosnakecase
		pk, err = json.Marshal(res)
		if err != nil {
			err = fmt.Errorf("failed to marshal public key %s as JSON: %w", in.Name, err)
			err = messages.ErrCryptoGetKey.WithFormat(in.Name, err)
			a.Logger.Debug(err)
			return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
		}
	}

	return &runtimev1pb.SubtleGetKeyAlpha1Response{
		Name:      kid,
		PublicKey: string(pk),
	}, nil
}

type subtleEncryptRes struct {
	ciphertext []byte
	tag        []byte
}

// SubtleEncryptAlpha1 encrypts a small message using a key stored in the vault.
func (a *UniversalAPI) SubtleEncryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleEncryptAlpha1Request) (*runtimev1pb.SubtleEncryptAlpha1Response, error) {
	component, err := a.CryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleEncryptAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[subtleEncryptRes](ctx,
		a.Resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	ser, err := policyRunner(func(ctx context.Context) (r subtleEncryptRes, rErr error) {
		r.ciphertext, r.tag, rErr = component.Encrypt(ctx, in.Plaintext, in.Algorithm, in.KeyName, in.Nonce, in.AssociatedData)
		return
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		// We are not going to return the exact error from the component to the user, because an error that is too specific could allow for various side channel attacks (e.g. AES-CBC and padding oracle attacks)
		// We will log the full error as a debug log, but only return a generic one to the user
		a.Logger.Debug(messages.ErrCryptoOperation.WithFormat(err))
		err = messages.ErrCryptoOperation.WithFormat("failed to encrypt")
		return &runtimev1pb.SubtleEncryptAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleEncryptAlpha1Response{
		Ciphertext: ser.ciphertext,
		Tag:        ser.tag,
	}, nil
}

// SubtleDecryptAlpha1 decrypts a small message using a key stored in the vault.
func (a *UniversalAPI) SubtleDecryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleDecryptAlpha1Request) (*runtimev1pb.SubtleDecryptAlpha1Response, error) {
	component, err := a.CryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleDecryptAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[[]byte](ctx,
		a.Resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	plaintext, err := policyRunner(func(ctx context.Context) ([]byte, error) {
		return component.Decrypt(ctx, in.Ciphertext, in.Algorithm, in.KeyName, in.Nonce, in.Tag, in.AssociatedData)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		// We are not going to return the exact error from the component to the user, because an error that is too specific could allow for various side channel attacks (e.g. AES-CBC and padding oracle attacks)
		// We will log the full error as a debug log, but only return a generic one to the user
		a.Logger.Debug(messages.ErrCryptoOperation.WithFormat(err))
		err = messages.ErrCryptoOperation.WithFormat("failed to decrypt")
		return &runtimev1pb.SubtleDecryptAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleDecryptAlpha1Response{
		Plaintext: plaintext,
	}, nil
}

type subtleWrapKeyRes struct {
	wrappedKey []byte
	tag        []byte
}

// SubtleWrapKeyAlpha1 wraps a key using a key stored in the vault.
func (a *UniversalAPI) SubtleWrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleWrapKeyAlpha1Request) (*runtimev1pb.SubtleWrapKeyAlpha1Response, error) {
	component, err := a.CryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleWrapKeyAlpha1Response{}, err
	}

	// Parse the plaintext key
	pk, err := kitCrypto.ParseKey(in.PlaintextKey, "")
	if err != nil {
		err = fmt.Errorf("failed to parse plaintext key: %w", err)
		err = messages.ErrCryptoOperation.WithFormat(err)
		a.Logger.Debug(err)
		return &runtimev1pb.SubtleWrapKeyAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[subtleWrapKeyRes](ctx,
		a.Resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	swkr, err := policyRunner(func(ctx context.Context) (r subtleWrapKeyRes, rErr error) {
		r.wrappedKey, r.tag, rErr = component.WrapKey(ctx, pk, in.Algorithm, in.KeyName, in.Nonce, in.AssociatedData)
		return
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		// We are not going to return the exact error from the component to the user, because an error that is too specific could allow for various side channel attacks (e.g. AES-CBC and padding oracle attacks)
		// We will log the full error as a debug log, but only return a generic one to the user
		a.Logger.Debug(messages.ErrCryptoOperation.WithFormat(err))
		err = messages.ErrCryptoOperation.WithFormat("failed to wrap key")
		return &runtimev1pb.SubtleWrapKeyAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleWrapKeyAlpha1Response{
		WrappedKey: swkr.wrappedKey,
		Tag:        swkr.tag,
	}, nil
}

// SubtleUnwrapKeyAlpha1 unwraps a key using a key stored in the vault.
func (a *UniversalAPI) SubtleUnwrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleUnwrapKeyAlpha1Request) (*runtimev1pb.SubtleUnwrapKeyAlpha1Response, error) {
	component, err := a.CryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[jwk.Key](ctx,
		a.Resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	plaintextText, err := policyRunner(func(ctx context.Context) (jwk.Key, error) {
		return component.UnwrapKey(ctx, in.WrappedKey, in.Algorithm, in.KeyName, in.Nonce, in.Tag, in.AssociatedData)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		// We are not going to return the exact error from the component to the user, because an error that is too specific could allow for various side channel attacks (e.g. AES-CBC and padding oracle attacks)
		// We will log the full error as a debug log, but only return a generic one to the user
		a.Logger.Debug(messages.ErrCryptoOperation.WithFormat(err))
		err = messages.ErrCryptoOperation.WithFormat("failed to unwrap key")
		return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{}, err
	}

	// Serialize the key
	enc, err := kitCrypto.SerializeKey(plaintextText)
	if err != nil {
		err = fmt.Errorf("failed to serialize unwrapped key: %w", err)
		err = messages.ErrCryptoOperation.WithFormat(err)
		a.Logger.Debug(err)
		return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{
		PlaintextKey: enc,
	}, nil
}

// SubtleSignAlpha1 signs a message using a key stored in the vault.
func (a *UniversalAPI) SubtleSignAlpha1(ctx context.Context, in *runtimev1pb.SubtleSignAlpha1Request) (*runtimev1pb.SubtleSignAlpha1Response, error) {
	component, err := a.CryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleSignAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[[]byte](ctx,
		a.Resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	sig, err := policyRunner(func(ctx context.Context) ([]byte, error) {
		return component.Sign(ctx, in.Digest, in.Algorithm, in.KeyName)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		// We are not going to return the exact error from the component to the user, because an error that is too specific could allow for various side channel attacks (e.g. AES-CBC and padding oracle attacks)
		// We will log the full error as a debug log, but only return a generic one to the user
		a.Logger.Debug(messages.ErrCryptoOperation.WithFormat(err))
		err = messages.ErrCryptoOperation.WithFormat("failed to sign")
		return &runtimev1pb.SubtleSignAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleSignAlpha1Response{
		Signature: sig,
	}, nil
}

// SubtleVerifyAlpha1 verifies the signature of a message using a key stored in the vault.
func (a *UniversalAPI) SubtleVerifyAlpha1(ctx context.Context, in *runtimev1pb.SubtleVerifyAlpha1Request) (*runtimev1pb.SubtleVerifyAlpha1Response, error) {
	component, err := a.CryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleVerifyAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[bool](ctx,
		a.Resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	valid, err := policyRunner(func(ctx context.Context) (bool, error) {
		return component.Verify(ctx, in.Digest, in.Signature, in.Algorithm, in.KeyName)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		// We are not going to return the exact error from the component to the user, because an error that is too specific could allow for various side channel attacks (e.g. AES-CBC and padding oracle attacks)
		// We will log the full error as a debug log, but only return a generic one to the user
		a.Logger.Debug(messages.ErrCryptoOperation.WithFormat(err))
		err = messages.ErrCryptoOperation.WithFormat("failed to verify signature")
		return &runtimev1pb.SubtleVerifyAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleVerifyAlpha1Response{
		Valid: valid,
	}, nil
}

// CryptoValidateRequest is an internal method that checks if the request is for a valid crypto component.
func (a *UniversalAPI) CryptoValidateRequest(componentName string) (contribCrypto.SubtleCrypto, error) {
	if len(a.CryptoProviders) == 0 {
		err := messages.ErrCryptoProvidersNotConfigured
		a.Logger.Debug(err)
		return nil, err
	}

	component := a.CryptoProviders[componentName]
	if component == nil {
		err := messages.ErrCryptoProviderNotFound.WithFormat(componentName)
		a.Logger.Debug(err)
		return nil, err
	}

	return component, nil
}
