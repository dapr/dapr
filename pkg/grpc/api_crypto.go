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

// SubtleGetKeyAlpha1 returns the public part of an asymmetric key stored in the vault.
func (a *api) SubtleGetKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleGetKeyAlpha1Request) (*runtimev1pb.SubtleGetKeyAlpha1Response, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
	}

	// Get the key
	policyRunner := resiliency.NewRunner[jwk.Key](ctx,
		a.resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	res, err := policyRunner(func(ctx context.Context) (jwk.Key, error) {
		return component.GetKey(ctx, in.Name)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.Get, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoGetKey, in.Name, err.Error())
		apiServerLogger.Debug(err)
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
			err = status.Errorf(codes.Internal, "failed to marshal public key %s as PKIX: %s", in.Name, err.Error())
			apiServerLogger.Debug(err)
			return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
		}
		der, err = x509.MarshalPKIXPublicKey(v)
		if err != nil {
			err = status.Errorf(codes.Internal, "failed to marshal public key %s as PKIX: %s", in.Name, err.Error())
			apiServerLogger.Debug(err)
			return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
		}
		pk = pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: der,
		})

	case runtimev1pb.SubtleGetKeyAlpha1Request_JSON: //nolint:nosnakecase
		pk, err = json.Marshal(res)
		if err != nil {
			err = status.Errorf(codes.Internal, "failed to marshal public key %s as JSON: %s", in.Name, err.Error())
			apiServerLogger.Debug(err)
			return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
		}

	default:
		err = status.Errorf(codes.InvalidArgument, "invalid key format")
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleGetKeyAlpha1Response{}, err
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
func (a *api) SubtleEncryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleEncryptAlpha1Request) (*runtimev1pb.SubtleEncryptAlpha1Response, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleEncryptAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[*subtleEncryptRes](ctx,
		a.resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	ser, err := policyRunner(func(ctx context.Context) (*subtleEncryptRes, error) {
		ciphertext, tag, rErr := component.Encrypt(ctx, in.Plaintext, in.Algorithm, in.Key, in.Nonce, in.AssociatedData)
		if rErr != nil {
			return nil, rErr
		}
		return &subtleEncryptRes{
			ciphertext: ciphertext,
			tag:        tag,
		}, nil
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleEncryptAlpha1Response{}, err
	}

	if ser == nil {
		ser = &subtleEncryptRes{}
	}
	return &runtimev1pb.SubtleEncryptAlpha1Response{
		Ciphertext: ser.ciphertext,
		Tag:        ser.tag,
	}, nil
}

// SubtleDecryptAlpha1 decrypts a small message using a key stored in the vault.
func (a *api) SubtleDecryptAlpha1(ctx context.Context, in *runtimev1pb.SubtleDecryptAlpha1Request) (*runtimev1pb.SubtleDecryptAlpha1Response, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleDecryptAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[[]byte](ctx,
		a.resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	plaintext, err := policyRunner(func(ctx context.Context) ([]byte, error) {
		return component.Decrypt(ctx, in.Ciphertext, in.Algorithm, in.Key, in.Nonce, in.Tag, in.AssociatedData)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
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
func (a *api) SubtleWrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleWrapKeyAlpha1Request) (*runtimev1pb.SubtleWrapKeyAlpha1Response, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleWrapKeyAlpha1Response{}, err
	}

	// Parse the plaintext key
	pk, err := contribCrypto.ParseKey(in.PlaintextKey, "")
	if err != nil {
		err = status.Errorf(codes.InvalidArgument, "error while parsing plaintext key: %s", err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleWrapKeyAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[*subtleWrapKeyRes](ctx,
		a.resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	swkr, err := policyRunner(func(ctx context.Context) (*subtleWrapKeyRes, error) {
		wrappedKey, tag, rErr := component.WrapKey(ctx, pk, in.Algorithm, in.Key, in.Nonce, in.AssociatedData)
		if rErr != nil {
			return nil, rErr
		}
		return &subtleWrapKeyRes{
			wrappedKey: wrappedKey,
			tag:        tag,
		}, nil
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleWrapKeyAlpha1Response{}, err
	}

	if swkr == nil {
		swkr = &subtleWrapKeyRes{}
	}
	return &runtimev1pb.SubtleWrapKeyAlpha1Response{
		WrappedKey: swkr.wrappedKey,
		Tag:        swkr.tag,
	}, nil
}

// SubtleUnwrapKeyAlpha1 unwraps a key using a key stored in the vault.
func (a *api) SubtleUnwrapKeyAlpha1(ctx context.Context, in *runtimev1pb.SubtleUnwrapKeyAlpha1Request) (*runtimev1pb.SubtleUnwrapKeyAlpha1Response, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[jwk.Key](ctx,
		a.resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	plaintextText, err := policyRunner(func(ctx context.Context) (jwk.Key, error) {
		return component.UnwrapKey(ctx, in.WrappedKey, in.Algorithm, in.Key, in.Nonce, in.Tag, in.AssociatedData)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{}, err
	}

	// Serialize the key
	enc, err := contribCrypto.SerializeKey(plaintextText)
	if err != nil {
		err = status.Errorf(codes.Internal, "failed to serialize unwrapped key: %s", err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleUnwrapKeyAlpha1Response{
		PlaintextKey: enc,
	}, nil
}

// SubtleSignAlpha1 signs a message using a key stored in the vault.
func (a *api) SubtleSignAlpha1(ctx context.Context, in *runtimev1pb.SubtleSignAlpha1Request) (*runtimev1pb.SubtleSignAlpha1Response, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleSignAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[[]byte](ctx,
		a.resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	sig, err := policyRunner(func(ctx context.Context) ([]byte, error) {
		return component.Sign(ctx, in.Digest, in.Algorithm, in.Key)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleSignAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleSignAlpha1Response{
		Signature: sig,
	}, nil
}

// SubtleVerifyAlpha1 verifies the signature of a message using a key stored in the vault.
func (a *api) SubtleVerifyAlpha1(ctx context.Context, in *runtimev1pb.SubtleVerifyAlpha1Request) (*runtimev1pb.SubtleVerifyAlpha1Response, error) {
	component, err := a.cryptoValidateRequest(in.ComponentName)
	if err != nil {
		return &runtimev1pb.SubtleVerifyAlpha1Response{}, err
	}

	policyRunner := resiliency.NewRunner[bool](ctx,
		a.resiliency.ComponentOutboundPolicy(in.ComponentName, resiliency.Crypto),
	)
	start := time.Now()
	valid, err := policyRunner(func(ctx context.Context) (bool, error) {
		return component.Verify(ctx, in.Digest, in.Signature, in.Algorithm, in.Key)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.CryptoInvoked(ctx, in.ComponentName, diag.CryptoOp, err == nil, elapsed)

	if err != nil {
		err = status.Errorf(codes.Internal, messages.ErrCryptoOperation, err.Error())
		apiServerLogger.Debug(err)
		return &runtimev1pb.SubtleVerifyAlpha1Response{}, err
	}

	return &runtimev1pb.SubtleVerifyAlpha1Response{
		Valid: valid,
	}, nil
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
