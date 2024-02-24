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

package errors

import (
	"net/http"

	grpcCodes "google.golang.org/grpc/codes"

	kiterrors "github.com/dapr/kit/errors"
)

const (
	// Crypto Errors
	ErrCryptoNameEmpty        = "NAME_EMPTY"
	ErrCryptoGetKey           = "GET_KEY"
	ErrCryptoEncryptOperation = "ENCRYPT"
	ErrCryptoDecryptOperation = "DECRYPT"
	ErrCryptoKeyOperation     = "KEY"
	ErrCryptoVerifySignature  = "VERIFY_SIGNATURE"

	// Crypto Error Messages
	MessageNameEmpty    = "crypto provider name empty"
	MessageEncryptError = "failed to perform encrypt operation: "
	MessageDecryptError = "failed to perform decrypt operation: "
)

const ResourceType = "Cryptography"

func CryptoNameEmpty(name string) error {
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		MessageNameEmpty,
		"ERR_CRYPTO_MISSING_COMPONENT_NAME",
	).
		WithErrorInfo(kiterrors.CodePrefixCryptography+ErrCryptoNameEmpty, nil).
		WithResourceInfo(ResourceType, name, "", MessageNameEmpty).
		Build()
}

func CryptoProviderGetKey(name string, err error) error {
	message := err.Error()
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_CRYPTO_PROVIDER_GET_KEY",
	).
		WithErrorInfo(kiterrors.CodePrefixCryptography+ErrCryptoGetKey, nil).
		WithResourceInfo("", name, "", message).
		Build()
}

func CryptoProviderEncryptOperation(name string, err error) error {
	message := MessageEncryptError + err.Error()
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_CRYPTO_ENCRYPT_OPERATION",
	).
		WithErrorInfo(kiterrors.CodePrefixBindings+ErrCryptoEncryptOperation, nil).
		WithResourceInfo("", name, "", message).
		Build()
}

func CryptoProviderDecryptOperation(name string, err error) error {
	message := MessageDecryptError + err.Error()
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_CRYPTO_DECRYPT_OPERATION",
	).
		WithErrorInfo(kiterrors.CodePrefixBindings+ErrCryptoDecryptOperation, nil).
		WithResourceInfo("", name, "", message).
		Build()
}

func CryptoProviderKeyOperation(name string, err error) error {
	message := err.Error()
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_CRYPTO_KEY_OPERATION",
	).
		WithErrorInfo(kiterrors.CodePrefixBindings+ErrCryptoKeyOperation, nil).
		WithResourceInfo("", name, "", message).
		Build()
}

func CryptoProviderVerifySignatureOperation(name string, err error) error {
	message := err.Error()
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_CRYPTO_VERIFY_SIGNATURE_OPERATION",
	).
		WithErrorInfo(kiterrors.CodePrefixBindings+ErrCryptoVerifySignature, nil).
		WithResourceInfo("", name, "", message).
		Build()
}
