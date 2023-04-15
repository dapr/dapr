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
	"bytes"
	"io"
	"strings"

	"github.com/valyala/fasthttp"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/ptr"
	encv1 "github.com/dapr/kit/schemes/enc/v1"
)

func (a *api) constructCryptoEndpoints() []Endpoint {
	// These APIs are not implemented as Universal because the gRPC APIs are stream-based.
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPut},
			Route:   "crypto/{name}/encrypt",
			Version: apiVersionV1alpha1,
			Handler: a.onCryptoEncrypt,
		},
		{
			Methods: []string{fasthttp.MethodPut},
			Route:   "crypto/{name}/decrypt",
			Version: apiVersionV1alpha1,
			Handler: a.onCryptoDecrypt,
		},
	}
}

// Handler for crypto/<component-name>/encrypt
// Query-string args:
// - keyName (required)
// - algorithm (required)
// - omitKeyName
// - decryptionKeyName
// - cipher
func (a *api) onCryptoEncrypt(reqCtx *fasthttp.RequestCtx) {
	// Get the component
	componentName := reqCtx.UserValue(nameParam).(string)
	component, err := a.cryptoGetComponent(componentName)
	if err != nil {
		// Error has been logged already
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	// Get the required properties from the query string
	keyName := string(reqCtx.QueryArgs().Peek("keyName"))
	if keyName == "" {
		err = messages.ErrBadRequest.WithFormat("missing query string parameter 'keyName'")
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}
	algorithm := string(reqCtx.QueryArgs().Peek("algorithm"))
	if algorithm == "" {
		err = messages.ErrBadRequest.WithFormat("missing query string parameter 'algorithm'")
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	// Ensure we have the required query string args
	encOpts := encv1.EncryptOptions{
		KeyName:   keyName,
		Algorithm: encv1.KeyAlgorithm(strings.ToUpper(algorithm)),
		WrapKeyFn: a.universal.CryptoGetWrapKeyFn(reqCtx, componentName, component),

		// The next values are optional and could be empty
		OmitKeyName:       utils.IsTruthy(string(reqCtx.QueryArgs().Peek("omitKeyName"))),
		DecryptionKeyName: string(reqCtx.QueryArgs().Peek("decryptionKeyName")),
	}

	// Set the cipher if present
	cipher := string(reqCtx.QueryArgs().Peek("cipher"))
	if cipher != "" {
		encOpts.Cipher = ptr.Of(encv1.Cipher(strings.ToUpper(cipher)))
	}

	// Get the body of the request
	// The body is released when this handler returns, so in order to be safe we need to create a copy of the data (yes, this means doubling the memory usage)
	// TODO: When we have proper support for streaming, read the data as a stream
	bodyOrig := reqCtx.Request.Body()
	body := make([]byte, len(bodyOrig))
	copy(body, bodyOrig)

	// Perform the encryption
	// Errors returned here, synchronously, are initialization errors, for example due to failed wrapping
	enc, err := encv1.Encrypt(bytes.NewReader(body), encOpts)
	if err != nil {
		err = messages.ErrCryptoOperation.WithFormat(err)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	// Respond with the encrypted data
	resBody, err := io.ReadAll(enc)
	if err != nil {
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}
	reqCtx.Response.Header.SetContentType("application/octet-stream")
	respond(reqCtx, with(fasthttp.StatusOK, resBody))
}

// Handler for crypto/<component-name>/decrypt
// Query-string args:
// - keyName
func (a *api) onCryptoDecrypt(reqCtx *fasthttp.RequestCtx) {
	// Get the component
	componentName := reqCtx.UserValue(nameParam).(string)
	component, err := a.cryptoGetComponent(componentName)
	if err != nil {
		// Error has been logged already
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	// Ensure we have the required query string args
	decOpts := encv1.DecryptOptions{
		UnwrapKeyFn: a.universal.CryptoGetUnwrapKeyFn(reqCtx, componentName, component),

		// The next values are optional and could be empty
		KeyName: string(reqCtx.QueryArgs().Peek("keyName")),
	}

	// Get the body of the request
	// The body is released when this handler returns, so in order to be safe we need to create a copy of the data (yes, this means doubling the memory usage)
	// TODO: When we have proper support for streaming, read the data as a stream
	bodyOrig := reqCtx.Request.Body()
	body := make([]byte, len(bodyOrig))
	copy(body, bodyOrig)

	// Perform the encryption
	// Errors returned here, synchronously, are initialization errors, for example due to failed unwrapping
	dec, err := encv1.Decrypt(bytes.NewReader(body), decOpts)
	if err != nil {
		err = messages.ErrCryptoOperation.WithFormat(err)
		log.Debug(err)
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}

	// Respond with the decrypted data
	resBody, err := io.ReadAll(dec)
	if err != nil {
		universalFastHTTPErrorResponder(reqCtx, err)
		return
	}
	reqCtx.Response.Header.SetContentType("application/octet-stream")
	respond(reqCtx, with(fasthttp.StatusOK, resBody))
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
