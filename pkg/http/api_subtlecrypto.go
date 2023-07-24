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
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dapr/dapr/pkg/http/endpoints"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *api) constructSubtleCryptoEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "subtlecrypto/{name}/getkey",
			Version: apiVersionV1alpha1,
			Group: endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupSubtleCrypto,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Name:    "SubtleGetKey",
			Handler: a.onPostSubtleCryptoGetKey(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "subtlecrypto/{name}/encrypt",
			Version: apiVersionV1alpha1,
			Group: endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupSubtleCrypto,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Name:    "SubtleEncrypt",
			Handler: a.onPostSubtleCryptoEncrypt(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "subtlecrypto/{name}/decrypt",
			Version: apiVersionV1alpha1,
			Group: endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupSubtleCrypto,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Name:    "SubtleDecrypt",
			Handler: a.onPostSubtleCryptoDecrypt(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "subtlecrypto/{name}/wrapkey",
			Version: apiVersionV1alpha1,
			Group: endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupSubtleCrypto,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Name:    "SubtleWrapKey",
			Handler: a.onPostSubtleCryptoWrapKey(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "subtlecrypto/{name}/unwrapkey",
			Version: apiVersionV1alpha1,
			Group: endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupSubtleCrypto,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Name:    "SubtleUnwrapKey",
			Handler: a.onPostSubtleCryptoUnwrapKey(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "subtlecrypto/{name}/sign",
			Version: apiVersionV1alpha1,
			Group: endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupSubtleCrypto,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Name:    "SubtleSign",
			Handler: a.onPostSubtleCryptoSign(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "subtlecrypto/{name}/verify",
			Version: apiVersionV1alpha1,
			Group: endpoints.EndpointGroup{
				Name:                 endpoints.EndpointGroupSubtleCrypto,
				Version:              endpoints.EndpointGroupVersion1alpha1,
				AppendSpanAttributes: nil, // TODO
			},
			Name:    "SubtleVerify",
			Handler: a.onPostSubtleCryptoVerify(),
		},
	}
}

func (a *api) onPostSubtleCryptoGetKey() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SubtleGetKeyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleGetKeyRequest, *runtimev1pb.SubtleGetKeyResponse]{
			InModifier: subtleCryptoInModifier[*runtimev1pb.SubtleGetKeyRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoEncrypt() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SubtleEncryptAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleEncryptRequest, *runtimev1pb.SubtleEncryptResponse]{
			InModifier: subtleCryptoInModifier[*runtimev1pb.SubtleEncryptRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoDecrypt() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SubtleDecryptAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleDecryptRequest, *runtimev1pb.SubtleDecryptResponse]{
			InModifier: subtleCryptoInModifier[*runtimev1pb.SubtleDecryptRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoWrapKey() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SubtleWrapKeyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleWrapKeyRequest, *runtimev1pb.SubtleWrapKeyResponse]{
			InModifier: subtleCryptoInModifier[*runtimev1pb.SubtleWrapKeyRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoUnwrapKey() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SubtleUnwrapKeyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleUnwrapKeyRequest, *runtimev1pb.SubtleUnwrapKeyResponse]{
			InModifier: subtleCryptoInModifier[*runtimev1pb.SubtleUnwrapKeyRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoSign() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SubtleSignAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleSignRequest, *runtimev1pb.SubtleSignResponse]{
			InModifier: subtleCryptoInModifier[*runtimev1pb.SubtleSignRequest],
		},
	)
}

func (a *api) onPostSubtleCryptoVerify() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SubtleVerifyAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SubtleVerifyRequest, *runtimev1pb.SubtleVerifyResponse]{
			InModifier: subtleCryptoInModifier[*runtimev1pb.SubtleVerifyRequest],
		},
	)
}

// Shared InModifier method for all universal handlers for subtle crypto that adds the "ComponentName" property
func subtleCryptoInModifier[T runtimev1pb.SubtleCryptoRequests](r *http.Request, in T) (T, error) {
	in.SetComponentName(chi.URLParam(r, nameParam))
	return in, nil
}
