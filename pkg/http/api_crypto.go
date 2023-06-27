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

package http

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/ptr"
	encv1 "github.com/dapr/kit/schemes/enc/v1"
)

const (
	cryptoHeaderKeyName               = "dapr-key-name"
	cryptoHeaderKeyWrapAlgorithm      = "dapr-key-wrap-algorithm"
	cryptoHeaderOmitDecryptionKeyName = "dapr-omit-decryption-key-name"
	cryptoHeaderDecryptionKeyName     = "dapr-decryption-key-name"
	cryptoHeaderDataEncryptionCipher  = "dapr-data-encryption-cipher"
)

func (a *api) constructCryptoEndpoints() []Endpoint {
	// These APIs are not implemented as Universal because the gRPC APIs are stream-based.
	return []Endpoint{
		{
			Methods: []string{http.MethodPut},
			Route:   "crypto/{name}/encrypt",
			Version: apiVersionV1alpha1,
			Handler: a.onCryptoEncrypt,
		},
		{
			Methods: []string{http.MethodPut},
			Route:   "crypto/{name}/decrypt",
			Version: apiVersionV1alpha1,
			Handler: a.onCryptoDecrypt,
		},
	}
}

// Handler for crypto/<component-name>/encrypt
// Supported headers:
// - dapr-key-name (required)
// - dapr-key-wrap-algorithm (required)
// - dapr-omit-decryption-key-name
// - dapr-decryption-key-name
// - dapr-data-encryption-cipher
func (a *api) onCryptoEncrypt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get the component
	componentName := chi.URLParamFromCtx(ctx, nameParam)
	component, err := a.cryptoGetComponent(componentName)
	if err != nil {
		// Error has been logged already
		respondWithError(w, err)
		return
	}

	// Get the required properties from the headers
	keyName := r.Header.Get(cryptoHeaderKeyName)
	if keyName == "" {
		err = messages.ErrBadRequest.WithFormat("missing header '" + cryptoHeaderKeyName + "'")
		log.Debug(err)
		respondWithError(w, err)
		return
	}
	algorithm := r.Header.Get(cryptoHeaderKeyWrapAlgorithm)
	if algorithm == "" {
		err = messages.ErrBadRequest.WithFormat("missing header '" + cryptoHeaderKeyWrapAlgorithm + "'")
		log.Debug(err)
		respondWithError(w, err)
		return
	}

	// Ensure we have the required headerss
	encOpts := encv1.EncryptOptions{
		KeyName:   keyName,
		Algorithm: encv1.KeyAlgorithm(strings.ToUpper(algorithm)),
		WrapKeyFn: a.universal.CryptoGetWrapKeyFn(ctx, componentName, component),

		// The next values are optional and could be empty
		OmitKeyName:       utils.IsTruthy(r.Header.Get(cryptoHeaderOmitDecryptionKeyName)),
		DecryptionKeyName: r.Header.Get(cryptoHeaderDecryptionKeyName),
	}

	// Set the cipher if present
	cipher := r.Header.Get(cryptoHeaderDataEncryptionCipher)
	if cipher != "" {
		encOpts.Cipher = ptr.Of(encv1.Cipher(strings.ToUpper(cipher)))
	}

	// Perform the encryption
	// Errors returned here, synchronously, are initialization errors, for example due to failed wrapping
	enc, err := encv1.Encrypt(r.Body, encOpts)
	if err != nil {
		err = messages.ErrCryptoOperation.WithFormat(err)
		log.Debug(err)
		respondWithError(w, err)
		return
	}

	// Respond with the encrypted data
	w.Header().Set("content-type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, enc)
	if err != nil {
		err = messages.ErrCryptoOperation.WithFormat(err)
		log.Debug(err)
		respondWithError(w, err)
	}
}

// Handler for crypto/<component-name>/decrypt
// Headers:
// - dapr-key-name
func (a *api) onCryptoDecrypt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get the component
	componentName := chi.URLParamFromCtx(ctx, nameParam)
	component, err := a.cryptoGetComponent(componentName)
	if err != nil {
		// Error has been logged already
		respondWithError(w, err)
		return
	}

	// Build the options
	decOpts := encv1.DecryptOptions{
		UnwrapKeyFn: a.universal.CryptoGetUnwrapKeyFn(ctx, componentName, component),

		// The next values are optional and could be empty
		KeyName: r.Header.Get(cryptoHeaderKeyName),
	}

	// Perform the encryption
	// Errors returned here, synchronously, are initialization errors, for example due to failed unwrapping
	dec, err := encv1.Decrypt(r.Body, decOpts)
	if err != nil {
		err = messages.ErrCryptoOperation.WithFormat(err)
		log.Debug(err)
		respondWithError(w, err)
		return
	}

	// Respond with the decrypted data
	w.Header().Set("content-type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, dec)
	if err != nil {
		err = messages.ErrCryptoOperation.WithFormat(err)
		log.Debug(err)
		respondWithError(w, err)
	}
}

func (a *api) cryptoGetComponent(componentName string) (contribCrypto.SubtleCrypto, error) {
	if a.universal.CompStore.CryptoProvidersLen() == 0 {
		err := messages.ErrCryptoProvidersNotConfigured
		log.Debug(err)
		return nil, err
	}

	if componentName == "" {
		err := messages.ErrBadRequest.WithFormat("missing component name")
		log.Debug(err)
		return nil, err
	}

	component, ok := a.universal.CompStore.GetCryptoProvider(componentName)
	if !ok {
		err := messages.ErrCryptoProviderNotFound.WithFormat(componentName)
		log.Debug(err)
		return nil, err
	}

	return component, nil
}
