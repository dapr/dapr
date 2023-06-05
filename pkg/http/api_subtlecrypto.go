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
	"github.com/valyala/fasthttp"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructSubtleCryptoEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:         []string{fasthttp.MethodPost},
			Route:           "subtlecrypto/{name}/getkey",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPostSubtleCryptoGetKey(),
		},
		{
			Methods:         []string{fasthttp.MethodPost},
			Route:           "subtlecrypto/{name}/encrypt",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPostSubtleCryptoEncrypt(),
		},
		{
			Methods:         []string{fasthttp.MethodPost},
			Route:           "subtlecrypto/{name}/decrypt",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPostSubtleCryptoDecrypt(),
		},
		{
			Methods:         []string{fasthttp.MethodPost},
			Route:           "subtlecrypto/{name}/wrapkey",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPostSubtleCryptoWrapKey(),
		},
		{
			Methods:         []string{fasthttp.MethodPost},
			Route:           "subtlecrypto/{name}/unwrapkey",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPostSubtleCryptoUnwrapKey(),
		},
		{
			Methods:         []string{fasthttp.MethodPost},
			Route:           "subtlecrypto/{name}/sign",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPostSubtleCryptoSign(),
		},
		{
			Methods:         []string{fasthttp.MethodPost},
			Route:           "subtlecrypto/{name}/verify",
			Version:         apiVersionV1alpha1,
			FastHTTPHandler: a.onPostSubtleCryptoVerify(),
		},
	}
}

func (a *api) onPostSubtleCryptoGetKey() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SubtleGetKeyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleGetKeyRequest, *runtimev1pb.SubtleGetKeyResponse]{
			InModifierFastHTTP: subtleCryptoInModifier[*runtimev1pb.SubtleGetKeyRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoEncrypt() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SubtleEncryptAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleEncryptRequest, *runtimev1pb.SubtleEncryptResponse]{
			InModifierFastHTTP: subtleCryptoInModifier[*runtimev1pb.SubtleEncryptRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoDecrypt() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SubtleDecryptAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleDecryptRequest, *runtimev1pb.SubtleDecryptResponse]{
			InModifierFastHTTP: subtleCryptoInModifier[*runtimev1pb.SubtleDecryptRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoWrapKey() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SubtleWrapKeyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleWrapKeyRequest, *runtimev1pb.SubtleWrapKeyResponse]{
			InModifierFastHTTP: subtleCryptoInModifier[*runtimev1pb.SubtleWrapKeyRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoUnwrapKey() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SubtleUnwrapKeyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleUnwrapKeyRequest, *runtimev1pb.SubtleUnwrapKeyResponse]{
			InModifierFastHTTP: subtleCryptoInModifier[*runtimev1pb.SubtleUnwrapKeyRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoSign() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SubtleSignAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleSignRequest, *runtimev1pb.SubtleSignResponse]{
			InModifierFastHTTP: subtleCryptoInModifier[*runtimev1pb.SubtleSignRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoVerify() fasthttp.RequestHandler {
	return UniversalFastHTTPHandler(
		a.universal.SubtleVerifyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleVerifyRequest, *runtimev1pb.SubtleVerifyResponse]{
			InModifierFastHTTP: subtleCryptoInModifier[*runtimev1pb.SubtleVerifyRequest],
		},
	)
}

// Shared InModifier method for all universal handlers for subtle crypto that adds the "ComponentName" property
func subtleCryptoInModifier[T runtimev1pb.SubtleCryptoRequests](reqCtx *fasthttp.RequestCtx, in T) (T, error) {
	in.SetComponentName(reqCtx.UserValue(nameParam).(string))
	return in, nil
}
