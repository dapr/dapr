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
	"fmt"
	"net/http"

	grpcCodes "google.golang.org/grpc/codes"

	kiterrors "github.com/dapr/kit/errors"
)

const (
	ErrCryptoProviderNotConfigured = "NOT_CONFIGURED"
	ErrCryptoProviderNotFound      = "NOT_FOUND"
	ErrCryptoNameEmpty             = "NAME_EMPTY"
	ErrCryptoGetKey                = "GET_KEY"
	ErrCryptoEncryptOperation      = "ENCRYPT"
	ErrCryptoDecryptOperation      = "DECRYPT"
	ErrCryptoKeyOperation          = "KEY"
	ErrCryptoVerifySignature       = "VERIFY_SIGNATURE"
)

const ResourceType = "Cryptography"

func CryptoNotConfigured(name string) error {
	message := "crypto providers not configured"
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED",
	).
		WithErrorInfo(kiterrors.CodePrefixCryptography+ErrCryptoProviderNotConfigured, nil).
		WithResourceInfo(ResourceType, name, "", message).
		Build()
}

func CryptoNotFound(name string) error {
	message := "crypto provider not found"
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"ERR_CRYPTO_PROVIDER_NOT_FOUND",
	).
		WithErrorInfo(kiterrors.CodePrefixCryptography+ErrCryptoProviderNotFound, nil).
		WithResourceInfo(ResourceType, name, "", message).
		Build()
}

func CryptoNameEmpty(name string) error {
	message := "crypto provider name empty"
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_CRYPTO_MISSING_COMPONENT_NAME",
	).
		WithErrorInfo(kiterrors.CodePrefixCryptography+ErrCryptoNameEmpty, nil).
		WithResourceInfo(ResourceType, name, "", message).
		Build()
}

func CryptoProviderGetKey(name string, err string) error {
	message := err
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
	message := fmt.Sprintf("failed to perform encrypt operation: %s", err.Error())
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
	message := fmt.Sprintf("failed to perform decrypt operation: %s", err.Error())
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

func CryptoProviderKeyOperation(name string, err string) error {
	message := err
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

func CryptoProviderVerifySignatureOperation(name string, err string) error {
	message := err
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
