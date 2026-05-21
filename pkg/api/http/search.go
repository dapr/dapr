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
	"net/http"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

var endpointGroupSearchV1Alpha1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupSearch,
	Version:              endpoints.EndpointGroupVersion1alpha1,
	AppendSpanAttributes: nil,
}

func (a *api) constructSearchEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "search/{storeName}/indexes/{index}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSearchV1Alpha1,
			Handler: a.onCreateIndexAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "CreateIndexAlpha1",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "search/{storeName}/indexes/{index}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSearchV1Alpha1,
			Handler: a.onDropIndexAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "DropIndexAlpha1",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "search/{storeName}/indexes/{index}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSearchV1Alpha1,
			Handler: a.onDescribeIndexAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "DescribeIndexAlpha1",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "search/{storeName}/indexes",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSearchV1Alpha1,
			Handler: a.onListIndexesAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "ListIndexesAlpha1",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "search/{storeName}/indexes/{index}/documents",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSearchV1Alpha1,
			Handler: a.onIndexDocumentsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "IndexDocumentsAlpha1",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "search/{storeName}/indexes/{index}/documents",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSearchV1Alpha1,
			Handler: a.onDeleteDocumentsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "DeleteDocumentsAlpha1",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "search/{storeName}/indexes/{index}/query",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSearchV1Alpha1,
			Handler: a.onSearchAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "SearchAlpha1",
			},
		},
	}
}

func (a *api) onCreateIndexAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.CreateIndexAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.CreateIndexRequestAlpha1, *runtimev1pb.CreateIndexResponseAlpha1]{
			InModifier: setSearchIndexPathParams[*runtimev1pb.CreateIndexRequestAlpha1],
		},
	)
}

func (a *api) onDropIndexAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DropIndexAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.DropIndexRequestAlpha1, *emptypb.Empty]{
			InModifier: setSearchIndexPathParams[*runtimev1pb.DropIndexRequestAlpha1],
		},
	)
}

func (a *api) onDescribeIndexAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DescribeIndexAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.DescribeIndexRequestAlpha1, *runtimev1pb.DescribeIndexResponseAlpha1]{
			InModifier:    setSearchIndexPathParams[*runtimev1pb.DescribeIndexRequestAlpha1],
			SkipInputBody: true,
		},
	)
}

func (a *api) onListIndexesAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.ListIndexesAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ListIndexesRequestAlpha1, *runtimev1pb.ListIndexesResponseAlpha1]{
			InModifier: func(r *http.Request, in *runtimev1pb.ListIndexesRequestAlpha1) (*runtimev1pb.ListIndexesRequestAlpha1, error) {
				in.StoreName = chi.URLParam(r, storeNameParam)
				return in, nil
			},
			SkipInputBody: true,
		},
	)
}

func (a *api) onIndexDocumentsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.IndexDocumentsHTTPAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.IndexDocumentsRequestAlpha1, *runtimev1pb.IndexDocumentsResponseAlpha1]{
			InModifier: setSearchIndexPathParams[*runtimev1pb.IndexDocumentsRequestAlpha1],
		},
	)
}

func (a *api) onDeleteDocumentsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DeleteDocumentsAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.DeleteDocumentsRequestAlpha1, *runtimev1pb.DeleteDocumentsResponseAlpha1]{
			InModifier: setSearchIndexPathParams[*runtimev1pb.DeleteDocumentsRequestAlpha1],
		},
	)
}

func (a *api) onSearchAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.SearchHTTPAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.SearchRequestAlpha1, *runtimev1pb.SearchResponseAlpha1]{
			InModifier: setSearchIndexPathParams[*runtimev1pb.SearchRequestAlpha1],
		},
	)
}

func setSearchIndexPathParams[T any](r *http.Request, in T) (T, error) {
	storeName := chi.URLParam(r, storeNameParam)
	index := chi.URLParam(r, indexParam)
	switch req := any(in).(type) {
	case *runtimev1pb.CreateIndexRequestAlpha1:
		req.StoreName = storeName
		req.Index = index
	case *runtimev1pb.DropIndexRequestAlpha1:
		req.StoreName = storeName
		req.Index = index
	case *runtimev1pb.DescribeIndexRequestAlpha1:
		req.StoreName = storeName
		req.Index = index
	case *runtimev1pb.IndexDocumentsRequestAlpha1:
		req.StoreName = storeName
		req.Index = index
	case *runtimev1pb.DeleteDocumentsRequestAlpha1:
		req.StoreName = storeName
		req.Index = index
	case *runtimev1pb.SearchRequestAlpha1:
		req.StoreName = storeName
		req.Index = index
	}
	return in, nil
}
