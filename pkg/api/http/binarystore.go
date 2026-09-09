/*
Copyright 2025 The Dapr Authors
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

	"github.com/go-chi/chi/v5"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
)

var endpointGroupBinaryStoreV1Alpha1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupBinaryStore,
	Version:              endpoints.EndpointGroupVersion1alpha1,
	AppendSpanAttributes: nil,
}

func (a *api) constructBinaryStoreEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPut},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupBinaryStoreV1Alpha1,
			Handler: a.onBinaryStoreSet(true),
			Settings: endpoints.EndpointSettings{
				Name: "SetBinaryFile",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupBinaryStoreV1Alpha1,
			Handler: a.onBinaryStoreSet(false),
			Settings: endpoints.EndpointSettings{
				Name: "SetBinaryFile",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupBinaryStoreV1Alpha1,
			Handler: a.onBinaryStoreGet(),
			Settings: endpoints.EndpointSettings{
				Name: "GetBinaryFile",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupBinaryStoreV1Alpha1,
			Handler: a.onBinaryStoreDelete(),
			Settings: endpoints.EndpointSettings{
				Name: "DeleteBinaryFile",
			},
		},
	}
}

// onBinaryStoreSet returns a handler that persists the request body to the
// named file. When overwrite is true (PUT) existing files are replaced; when
// false (POST) the request fails with 409 if the file already exists. The body
// is streamed to the component without buffering in the sidecar.
func (a *api) onBinaryStoreSet(overwrite bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		componentName := chi.URLParamFromCtx(ctx, nameParam)
		fileName := chi.URLParamFromCtx(ctx, binaryStoreFileNameParam)

		// Defer closing the request body so the connection can be reused.
		defer func() { _ = r.Body.Close() }()

		err := a.universal.SetBinaryFileAlpha1(ctx, componentName, fileName, overwrite, r.Body)
		if err != nil {
			respondWithError(w, err)
			return
		}

		respondWithEmpty(w)
	}
}

// onBinaryStoreGet returns a handler that streams the named file back to the
// client with Content-Type application/octet-stream.
func (a *api) onBinaryStoreGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		componentName := chi.URLParamFromCtx(ctx, nameParam)
		fileName := chi.URLParamFromCtx(ctx, binaryStoreFileNameParam)

		body, err := a.universal.GetBinaryFileAlpha1(ctx, componentName, fileName)
		if err != nil {
			respondWithError(w, err)
			return
		}
		defer func() { _ = body.Close() }()

		// Headers must be set before the first Write; any error from the
		// provider is surfaced before streaming begins, so the correct status
		// code can be returned. Errors that occur mid-stream cannot change the
		// status code, which is an accepted trade-off for streaming responses.
		w.Header().Set(headerContentType, "application/octet-stream")
		w.WriteHeader(http.StatusOK)

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		buf := make([]byte, 32*1024)
		for {
			n, readErr := body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			if readErr == io.EOF {
				return
			}
			if readErr != nil {
				// Status code already sent; best-effort log only.
				log.Debugf("error streaming binary file %q from component %s: %s", fileName, componentName, readErr)
				return
			}
		}
	}
}

// onBinaryStoreDelete returns a handler that deletes the named file.
func (a *api) onBinaryStoreDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		componentName := chi.URLParamFromCtx(ctx, nameParam)
		fileName := chi.URLParamFromCtx(ctx, binaryStoreFileNameParam)

		if err := a.universal.DeleteBinaryFileAlpha1(ctx, componentName, fileName); err != nil {
			respondWithError(w, err)
			return
		}

		respondWithEmpty(w)
	}
}
