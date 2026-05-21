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

var endpointGroupVectorV1Alpha1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupVector,
	Version:              endpoints.EndpointGroupVersion1alpha1,
	AppendSpanAttributes: nil,
}

func (a *api) constructVectorEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "vector/{storeName}/collections/{collection}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onCreateCollectionAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "CreateCollectionAlpha1",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "vector/{storeName}/collections/{collection}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onDropCollectionAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "DropCollectionAlpha1",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "vector/{storeName}/collections/{collection}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onDescribeCollectionAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "DescribeCollectionAlpha1",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "vector/{storeName}/collections",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onListCollectionsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "ListCollectionsAlpha1",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "vector/{storeName}/collections/{collection}/upsert",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onUpsertVectorsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "UpsertVectorsAlpha1",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "vector/{storeName}/collections/{collection}/get",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onGetVectorsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "GetVectorsAlpha1",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "vector/{storeName}/collections/{collection}/vectors",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onDeleteVectorsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "DeleteVectorsAlpha1",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "vector/{storeName}/collections/{collection}/query",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onQueryVectorsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "QueryVectorsAlpha1",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "vector/{storeName}/collections/{collection}/batch-query",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupVectorV1Alpha1,
			Handler: a.onBatchQueryVectorsAlpha1(),
			Settings: endpoints.EndpointSettings{
				Name: "BatchQueryVectorsAlpha1",
			},
		},
	}
}

func (a *api) onCreateCollectionAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.CreateCollectionAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.CreateCollectionRequestAlpha1, *runtimev1pb.CreateCollectionResponseAlpha1]{
			InModifier: setVectorCollectionPathParams[*runtimev1pb.CreateCollectionRequestAlpha1],
		},
	)
}

func (a *api) onDropCollectionAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DropCollectionAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.DropCollectionRequestAlpha1, *emptypb.Empty]{
			InModifier: setVectorCollectionPathParams[*runtimev1pb.DropCollectionRequestAlpha1],
		},
	)
}

func (a *api) onDescribeCollectionAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DescribeCollectionAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.DescribeCollectionRequestAlpha1, *runtimev1pb.DescribeCollectionResponseAlpha1]{
			InModifier:    setVectorCollectionPathParams[*runtimev1pb.DescribeCollectionRequestAlpha1],
			SkipInputBody: true,
		},
	)
}

func (a *api) onListCollectionsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.ListCollectionsAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ListCollectionsRequestAlpha1, *runtimev1pb.ListCollectionsResponseAlpha1]{
			InModifier: func(r *http.Request, in *runtimev1pb.ListCollectionsRequestAlpha1) (*runtimev1pb.ListCollectionsRequestAlpha1, error) {
				in.StoreName = chi.URLParam(r, storeNameParam)
				return in, nil
			},
			SkipInputBody: true,
		},
	)
}

func (a *api) onUpsertVectorsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.UpsertVectorsHTTPAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.UpsertVectorsRequestAlpha1, *runtimev1pb.UpsertVectorsResponseAlpha1]{
			InModifier: setVectorCollectionPathParams[*runtimev1pb.UpsertVectorsRequestAlpha1],
		},
	)
}

func (a *api) onGetVectorsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.GetVectorsAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetVectorsRequestAlpha1, *runtimev1pb.GetVectorsResponseAlpha1]{
			InModifier: setVectorCollectionPathParams[*runtimev1pb.GetVectorsRequestAlpha1],
		},
	)
}

func (a *api) onDeleteVectorsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DeleteVectorsAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.DeleteVectorsRequestAlpha1, *runtimev1pb.DeleteVectorsResponseAlpha1]{
			InModifier: setVectorCollectionPathParams[*runtimev1pb.DeleteVectorsRequestAlpha1],
		},
	)
}

func (a *api) onQueryVectorsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.QueryVectorsHTTPAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.QueryVectorsRequestAlpha1, *runtimev1pb.QueryVectorsResponseAlpha1]{
			InModifier: setVectorCollectionPathParams[*runtimev1pb.QueryVectorsRequestAlpha1],
		},
	)
}

func (a *api) onBatchQueryVectorsAlpha1() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.BatchQueryVectorsAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.BatchQueryVectorsRequestAlpha1, *runtimev1pb.BatchQueryVectorsResponseAlpha1]{
			InModifier: func(r *http.Request, in *runtimev1pb.BatchQueryVectorsRequestAlpha1) (*runtimev1pb.BatchQueryVectorsRequestAlpha1, error) {
				storeName := chi.URLParam(r, storeNameParam)
				collection := chi.URLParam(r, collectionParam)
				in.StoreName = storeName
				in.Collection = collection
				for _, query := range in.GetQueries() {
					query.StoreName = storeName
					query.Collection = collection
				}
				return in, nil
			},
		},
	)
}

func setVectorCollectionPathParams[T any](r *http.Request, in T) (T, error) {
	storeName := chi.URLParam(r, storeNameParam)
	collection := chi.URLParam(r, collectionParam)
	switch req := any(in).(type) {
	case *runtimev1pb.CreateCollectionRequestAlpha1:
		req.StoreName = storeName
		req.Collection = collection
	case *runtimev1pb.DropCollectionRequestAlpha1:
		req.StoreName = storeName
		req.Collection = collection
	case *runtimev1pb.DescribeCollectionRequestAlpha1:
		req.StoreName = storeName
		req.Collection = collection
	case *runtimev1pb.UpsertVectorsRequestAlpha1:
		req.StoreName = storeName
		req.Collection = collection
	case *runtimev1pb.GetVectorsRequestAlpha1:
		req.StoreName = storeName
		req.Collection = collection
	case *runtimev1pb.DeleteVectorsRequestAlpha1:
		req.StoreName = storeName
		req.Collection = collection
	case *runtimev1pb.QueryVectorsRequestAlpha1:
		req.StoreName = storeName
		req.Collection = collection
	}
	return in, nil
}
