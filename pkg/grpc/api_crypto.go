package grpc

import (
	"context"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// SubtleGetKey returns the public part of an asymmetric key stored in the vault.
func (a *api) SubtleGetKey(context.Context, *runtimev1pb.SubtleGetKeyRequest) (*runtimev1pb.SubtleGetKeyResponse, error) {
	panic("unimplemented")
}

// SubtleEncrypt encrypts a small message using a key stored in the vault.
func (a *api) SubtleEncrypt(context.Context, *runtimev1pb.SubtleEncryptRequest) (*runtimev1pb.SubtleEncryptResponse, error) {
	panic("unimplemented")
}

// SubtleDecrypt decrypts a small message using a key stored in the vault.
func (a *api) SubtleDecrypt(context.Context, *runtimev1pb.SubtleDecryptRequest) (*runtimev1pb.SubtleDecryptResponse, error) {
	panic("unimplemented")
}

// SubtleWrapKey wraps a key using a key stored in the vault.
func (a *api) SubtleWrapKey(context.Context, *runtimev1pb.SubtleWrapKeyRequest) (*runtimev1pb.SubtleWrapKeyResponse, error) {
	panic("unimplemented")
}

// SubtleUnwrapKey unwraps a key using a key stored in the vault.
func (a *api) SubtleUnwrapKey(context.Context, *runtimev1pb.SubtleUnwrapKeyRequest) (*runtimev1pb.SubtleUnwrapKeyResponse, error) {
	panic("unimplemented")
}

// SubtleSign signs a message using a key stored in the vault.
func (a *api) SubtleSign(context.Context, *runtimev1pb.SubtleSignRequest) (*runtimev1pb.SubtleSignResponse, error) {
	panic("unimplemented")
}

// SubtleVerify verifies the signature of a message using a key stored in the vault.
func (a *api) SubtleVerify(context.Context, *runtimev1pb.SubtleVerifyRequest) (*runtimev1pb.SubtleVerifyResponse, error) {
	panic("unimplemented")
}
