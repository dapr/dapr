package grpc

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

// SubtleGetKey returns the public part of an asymmetric key stored in the vault.
func (a *api) SubtleGetKey(ctx context.Context, in *runtimev1pb.SubtleGetKeyRequest) (*runtimev1pb.SubtleGetKeyResponse, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleGetKeyResponse{}, err
	}

	// Get the key
	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.ComponentName, resiliency.Crypto)
	var res jwk.Key
	err = policy(func(ctx context.Context) (rErr error) {
		res, rErr = component.GetKey(ctx, in.Name)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoGetKey, in.Name, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleGetKeyResponse{}, err
	}

	// Get the key ID if present
	kid := in.Name
	if dk, ok := res.(*contribCrypto.Key); ok {
		kid = dk.KeyID()
	}

	// Format the response
	var pk []byte
	switch in.Format {
	case runtimev1pb.SubtleGetKeyRequest_PEM: //nolint:nosnakecase
		var v crypto.PublicKey
		err = res.Raw(&v)
		if err != nil {
			err = status.Errorf(codes.Internal, "failed to marshal public key %s as PKIX: %s", in.Name, err.Error())
			apiServerLogger.Debug(err)
			return &runtimev1pb.SubtleGetKeyResponse{}, err
		}
		der, err := x509.MarshalPKIXPublicKey(v)
		if err != nil {
			err = status.Errorf(codes.Internal, "failed to marshal public key %s as PKIX: %s", in.Name, err.Error())
			apiServerLogger.Debug(err)
			return &runtimev1pb.SubtleGetKeyResponse{}, err
		}
		pk = pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: der,
		})
	case runtimev1pb.SubtleGetKeyRequest_JSON: //nolint:nosnakecase
		pk, err = json.Marshal(res)
		if err != nil {
			err = status.Errorf(codes.Internal, "failed to marshal public key %s as JSON: %s", in.Name, err.Error())
			apiServerLogger.Debug(err)
			return &runtimev1pb.SubtleGetKeyResponse{}, err
		}
	}

	return &runtimev1pb.SubtleGetKeyResponse{
		Name:      kid,
		PublicKey: string(pk),
	}, nil
}

// SubtleEncrypt encrypts a small message using a key stored in the vault.
func (a *api) SubtleEncrypt(ctx context.Context, in *runtimev1pb.SubtleEncryptRequest) (*runtimev1pb.SubtleEncryptResponse, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleEncryptResponse{}, err
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.ComponentName, resiliency.Crypto)
	var (
		ciphertext []byte
		tag        []byte
	)
	err = policy(func(ctx context.Context) (rErr error) {
		ciphertext, tag, rErr = component.Encrypt(ctx, in.Plaintext, in.Algorithm, in.Key, in.Nonce, in.AssociatedData)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleEncryptResponse{}, err
	}

	return &runtimev1pb.SubtleEncryptResponse{
		Ciphertext: ciphertext,
		Tag:        tag,
	}, nil
}

// SubtleDecrypt decrypts a small message using a key stored in the vault.
func (a *api) SubtleDecrypt(ctx context.Context, in *runtimev1pb.SubtleDecryptRequest) (*runtimev1pb.SubtleDecryptResponse, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleDecryptResponse{}, err
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.ComponentName, resiliency.Crypto)
	var plaintext []byte
	err = policy(func(ctx context.Context) (rErr error) {
		plaintext, rErr = component.Decrypt(ctx, in.Ciphertext, in.Algorithm, in.Key, in.Nonce, in.Tag, in.AssociatedData)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleDecryptResponse{}, err
	}

	return &runtimev1pb.SubtleDecryptResponse{
		Plaintext: plaintext,
	}, nil
}

// SubtleWrapKey wraps a key using a key stored in the vault.
func (a *api) SubtleWrapKey(ctx context.Context, in *runtimev1pb.SubtleWrapKeyRequest) (*runtimev1pb.SubtleWrapKeyResponse, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleWrapKeyResponse{}, err
	}

	// Parse the plaintext key
	pk, err := contribCrypto.ParseKey(in.PlaintextKey, "")
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "error while parsing plaintext key: %s", err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleWrapKeyResponse{}, err
	}

	start := time.Now()
	policy := a.resiliency.ComponentOutboundPolicy(ctx, in.ComponentName, resiliency.Crypto)
	var (
		wrappedKey []byte
		tag        []byte
	)
	err = policy(func(ctx context.Context) (rErr error) {
		wrappedKey, tag, rErr = component.WrapKey(ctx, pk, in.Algorithm, in.Key, in.Nonce, in.AssociatedData)
		return rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleWrapKeyResponse{}, err
	}

	return &runtimev1pb.SubtleWrapKeyResponse{
		WrappedKey: wrappedKey,
		Tag:        tag,
	}, nil
}

// SubtleUnwrapKey unwraps a key using a key stored in the vault.
func (a *api) SubtleUnwrapKey(ctx context.Context, in *runtimev1pb.SubtleUnwrapKeyRequest) (*runtimev1pb.SubtleUnwrapKeyResponse, error) {
	panic("unimplemented")
}

// SubtleSign signs a message using a key stored in the vault.
func (a *api) SubtleSign(ctx context.Context, in *runtimev1pb.SubtleSignRequest) (*runtimev1pb.SubtleSignResponse, error) {
	panic("unimplemented")
}

// SubtleVerify verifies the signature of a message using a key stored in the vault.
func (a *api) SubtleVerify(ctx context.Context, in *runtimev1pb.SubtleVerifyRequest) (*runtimev1pb.SubtleVerifyResponse, error) {
	panic("unimplemented")
}

// Internal method that checks if the request is for a valid crypto component.
func (a *api) cryptoValidateRequest(componentName string) (contribCrypto.SubtleCrypto, error) {
	if a.cryptoProviders == nil || len(a.cryptoProviders) == 0 {
		err := status.Error(codes.FailedPrecondition, messages.ErrCryptoProvidersNotConfigured)
		apiServerLogger.Debug(err)
		return nil, err
	}

	component := a.cryptoProviders[componentName]
	if component == nil {
		err := status.Errorf(codes.InvalidArgument, messages.ErrCryptoProviderNotFound, componentName)
		apiServerLogger.Debug(err)
		return nil, err
	}

	return component, nil
}
