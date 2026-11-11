/*
Copyright 2026 The Dapr Authors
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
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/messages"
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
				Name:                     "SetBinaryFile",
				SkipRequestBodySizeLimit: true,
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "binarystore/{name}/{fileName}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupBinaryStoreV1Alpha1,
			Handler: a.onBinaryStoreSet(false),
			Settings: endpoints.EndpointSettings{
				Name:                     "SetBinaryFile",
				SkipRequestBodySizeLimit: true,
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

// binaryStoreNames extracts the component and file names from the request.
// CleanPathMiddleware normalizes the route path before matching, which
// silently turns e.g. /binarystore/mystore/a/../b.bin into a request for
// b.bin; reject dot segments in the original escaped path so HTTP callers
// get the same validation as gRPC callers.
func binaryStoreNames(r *http.Request) (componentName, fileName string, err error) {
	for segment := range strings.SplitSeq(r.URL.EscapedPath(), "/") {
		if segment == "." || segment == ".." {
			return "", "", messages.ErrBadRequest.WithFormat("binary store path %q must not contain '.' or '..' segments", r.URL.EscapedPath())
		}
	}
	ctx := r.Context()
	return chi.URLParamFromCtx(ctx, nameParam), chi.URLParamFromCtx(ctx, binaryStoreFileNameParam), nil
}

// onBinaryStoreSet returns a handler that persists the request body to the
// named file. When overwrite is true (PUT) existing files are replaced; when
// false (POST) the request fails with 409 if the file already exists. The body
// is streamed to the component without buffering in the sidecar.
func (a *api) onBinaryStoreSet(overwrite bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		componentName, fileName, err := binaryStoreNames(r)
		if err != nil {
			respondWithError(w, err)
			return
		}

		// Defer closing the request body so the connection can be reused.
		defer func() { _ = r.Body.Close() }()

		err = a.universal.SetBinaryFileAlpha1(ctx, componentName, fileName, overwrite, r.Body)
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
		componentName, fileName, err := binaryStoreNames(r)
		if err != nil {
			respondWithError(w, err)
			return
		}

		body, err := a.universal.GetBinaryFileAlpha1(r.Context(), componentName, fileName)
		if err != nil {
			respondWithError(w, err)
			return
		}
		defer func() { _ = body.Close() }()

		streamBinaryFile(w, body, componentName, fileName)
	}
}

func streamBinaryFile(w http.ResponseWriter, body io.Reader, componentName, fileName string) {
	// Headers must be set before the first Write. Errors that occur mid-stream
	// cannot change the status code, so the response is aborted to ensure the
	// client can detect a truncated body.
	w.Header().Set(headerContentType, "application/octet-stream")
	w.WriteHeader(http.StatusOK)

	var dst io.Writer = w
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
		dst = &flushWriter{w: w, f: f}
	}

	if _, err := io.Copy(dst, body); err != nil {
		log.Debugf("error streaming binary file %q from component %s: %s", fileName, componentName, err)
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now())
	}
}

// onBinaryStoreDelete returns a handler that deletes the named file.
func (a *api) onBinaryStoreDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		componentName, fileName, err := binaryStoreNames(r)
		if err != nil {
			respondWithError(w, err)
			return
		}

		if err := a.universal.DeleteBinaryFileAlpha1(r.Context(), componentName, fileName); err != nil {
			respondWithError(w, err)
			return
		}

		respondWithEmpty(w)
	}
}
