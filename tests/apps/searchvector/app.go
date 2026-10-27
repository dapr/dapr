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

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"

	chi "github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort     = 3000
	searchStore = "e2e-search"
	vectorStore = "e2e-vector"
)

var client runtimev1pb.DaprClient

// waitForCompletion makes every write return only once Meilisearch has
// applied it, so the next step can read it back.
var waitForCompletion = &runtimev1pb.IndexingOptionsAlpha1{
	Mode:          runtimev1pb.IndexingMode_INDEXING_MODE_WAIT_FOR_COMPLETION,
	WaitTimeout:   durationpb.New(30 * time.Second),
	OnWaitTimeout: runtimev1pb.IndexingWaitTimeoutAction_INDEXING_WAIT_TIMEOUT_ACTION_FAIL_REQUEST,
}

// testHandler runs one scenario against the sidecar. The first step that does
// not behave as expected fails the request with the reason in the body.
func testHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	name := "e2e-" + chi.URLParam(r, "runID")
	var err error
	switch scenario := chi.URLParam(r, "scenario"); scenario {
	case "search":
		err = searchScenario(ctx, name)
	case "vector":
		err = vectorScenario(ctx, name)
	default:
		http.Error(w, "unknown scenario "+scenario, http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("Scenario %s failed: %v", r.URL.Path, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func searchScenario(ctx context.Context, index string) error {
	_, err := client.CreateIndexAlpha1(ctx, &runtimev1pb.CreateIndexRequestAlpha1{
		StoreName: searchStore,
		Index:     index,
		Metadata:  map[string]string{"filterableAttributes": "category"},
	})
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer client.DeleteIndexAlpha1(context.Background(), &runtimev1pb.DeleteIndexRequestAlpha1{StoreName: searchStore, Index: index}) //nolint:errcheck

	indexed, err := client.IndexDocumentsAlpha1(ctx, &runtimev1pb.IndexDocumentsRequestAlpha1{
		StoreName: searchStore,
		Index:     index,
		Documents: []*runtimev1pb.SearchDocument{
			{Id: "doc-1", Content: []byte(`{"title":"alpha guide","category":"guide"}`)},
			{Id: "doc-2", Content: []byte(`{"title":"beta guide","category":"guide"}`)},
			{Id: "doc-3", Content: []byte(`{"title":"alpha reference","category":"reference"}`)},
		},
		Options: waitForCompletion,
	})
	if err != nil {
		return fmt.Errorf("index documents: %w", err)
	}
	if err = completed("index documents", indexed.GetAck(), len(indexed.GetFailedItems())); err != nil {
		return err
	}

	search := func(step string, query *runtimev1pb.SearchQuery, want ...string) error {
		query.TopK = new(uint32(10))
		resp, sErr := client.SearchAlpha1(ctx, &runtimev1pb.SearchRequestAlpha1{StoreName: searchStore, Index: index, Query: query})
		if sErr != nil {
			return fmt.Errorf("%s: %w", step, sErr)
		}
		ids := make([]string, 0, len(resp.GetHits()))
		for _, hit := range resp.GetHits() {
			ids = append(ids, hit.GetDocument().GetId())
		}
		return sameIDs(step, ids, want)
	}

	if err = search("search text", &runtimev1pb.SearchQuery{Kind: &runtimev1pb.SearchQuery_Text{Text: "alpha"}}, "doc-1", "doc-3"); err != nil {
		return err
	}
	filter, err := structpb.NewStruct(map[string]any{"category": "reference"})
	if err != nil {
		return err
	}
	if err = search("search filter", &runtimev1pb.SearchQuery{Filter: filter}, "doc-3"); err != nil {
		return err
	}

	got, err := client.GetDocumentsAlpha1(ctx, &runtimev1pb.GetDocumentsRequestAlpha1{StoreName: searchStore, Index: index, Ids: []string{"doc-1", "doc-2"}})
	if err != nil {
		return fmt.Errorf("get documents: %w", err)
	}
	ids := make([]string, 0, len(got.GetDocuments()))
	for _, doc := range got.GetDocuments() {
		ids = append(ids, doc.GetId())
	}
	if err = sameIDs("get documents", ids, []string{"doc-1", "doc-2"}); err != nil {
		return err
	}

	deleted, err := client.DeleteDocumentsAlpha1(ctx, &runtimev1pb.DeleteDocumentsRequestAlpha1{StoreName: searchStore, Index: index, Ids: []string{"doc-1"}, Options: waitForCompletion})
	if err != nil {
		return fmt.Errorf("delete documents: %w", err)
	}
	if err = completed("delete documents", deleted.GetAck(), 0); err != nil {
		return err
	}
	if err = search("search after delete", &runtimev1pb.SearchQuery{Kind: &runtimev1pb.SearchQuery_Text{Text: "alpha"}}, "doc-3"); err != nil {
		return err
	}

	if _, err = client.DeleteIndexAlpha1(ctx, &runtimev1pb.DeleteIndexRequestAlpha1{StoreName: searchStore, Index: index}); err != nil {
		return fmt.Errorf("delete index: %w", err)
	}
	_, err = client.GetIndexAlpha1(ctx, &runtimev1pb.GetIndexRequestAlpha1{StoreName: searchStore, Index: index})
	return notFound("get deleted index", err)
}

func vectorScenario(ctx context.Context, collection string) error {
	_, err := client.CreateCollectionAlpha1(ctx, &runtimev1pb.CreateCollectionRequestAlpha1{
		StoreName:  vectorStore,
		Collection: collection,
		Dimensions: 4,
		Metric:     runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		// Meilisearch stores record metadata under daprMetadata, which must be
		// declared filterable for metadata filters to apply.
		Metadata: map[string]string{"filterableAttributes": "daprMetadata"},
	})
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	defer client.DeleteCollectionAlpha1(context.Background(), &runtimev1pb.DeleteCollectionRequestAlpha1{StoreName: vectorStore, Collection: collection}) //nolint:errcheck

	records := make([]*runtimev1pb.VectorRecord, 0, 3)
	for _, r := range []struct {
		id     string
		values []float32
		tenant string
	}{
		{"vec-1", []float32{1, 0, 0, 0}, "red"},
		{"vec-2", []float32{0.9, 0.1, 0, 0}, "red"},
		{"vec-3", []float32{0, 1, 0, 0}, "blue"},
	} {
		md, mdErr := structpb.NewStruct(map[string]any{"tenant": r.tenant})
		if mdErr != nil {
			return mdErr
		}
		records = append(records, &runtimev1pb.VectorRecord{Id: r.id, Values: r.values, Metadata: md})
	}
	upserted, err := client.UpsertVectorsAlpha1(ctx, &runtimev1pb.UpsertVectorsRequestAlpha1{StoreName: vectorStore, Collection: collection, Records: records, Options: waitForCompletion})
	if err != nil {
		return fmt.Errorf("upsert vectors: %w", err)
	}
	if err = completed("upsert vectors", upserted.GetAck(), len(upserted.GetFailedItems())); err != nil {
		return err
	}

	query := func(values []float32, topK uint32, filter map[string]any) (*runtimev1pb.VectorQuery, error) {
		q := &runtimev1pb.VectorQuery{
			Kind:   &runtimev1pb.VectorQuery_Vector{Vector: &runtimev1pb.VectorRecord{Values: values}},
			TopK:   &topK,
			Metric: runtimev1pb.DistanceMetric_DISTANCE_METRIC_COSINE,
		}
		if filter != nil {
			var fErr error
			if q.Filter, fErr = structpb.NewStruct(filter); fErr != nil {
				return nil, fErr
			}
		}
		return q, nil
	}
	matchIDs := func(matches []*runtimev1pb.VectorMatch) []string {
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.GetRecord().GetId())
		}
		return ids
	}

	nearest, err := query([]float32{1, 0, 0, 0}, 2, nil)
	if err != nil {
		return err
	}
	resp, err := client.QueryVectorsAlpha1(ctx, &runtimev1pb.QueryVectorsRequestAlpha1{StoreName: vectorStore, Collection: collection, Query: nearest})
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	if ids := matchIDs(resp.GetMatches()); len(ids) != 2 || ids[0] != "vec-1" {
		return fmt.Errorf("query: got matches %v, want vec-1 first of two", ids)
	}

	blue, err := query([]float32{1, 0, 0, 0}, 3, map[string]any{"tenant": "blue"})
	if err != nil {
		return err
	}
	resp, err = client.QueryVectorsAlpha1(ctx, &runtimev1pb.QueryVectorsRequestAlpha1{StoreName: vectorStore, Collection: collection, Query: blue})
	if err != nil {
		return fmt.Errorf("query filter: %w", err)
	}
	if err = sameIDs("query filter", matchIDs(resp.GetMatches()), []string{"vec-3"}); err != nil {
		return err
	}

	first, err := query([]float32{1, 0, 0, 0}, 1, nil)
	if err != nil {
		return err
	}
	second, err := query([]float32{0, 1, 0, 0}, 1, nil)
	if err != nil {
		return err
	}
	batch, err := client.BatchQueryVectorsAlpha1(ctx, &runtimev1pb.BatchQueryVectorsRequestAlpha1{StoreName: vectorStore, Collection: collection, Queries: []*runtimev1pb.VectorQuery{first, second}})
	if err != nil {
		return fmt.Errorf("batch query: %w", err)
	}
	var batchIDs []string
	for _, result := range batch.GetResults() {
		if result.GetError() != nil {
			return fmt.Errorf("batch query: per-query error %v", result.GetError())
		}
		batchIDs = append(batchIDs, matchIDs(result.GetResponse().GetMatches())...)
	}
	if !slices.Equal(batchIDs, []string{"vec-1", "vec-3"}) {
		return fmt.Errorf("batch query: got %v, want [vec-1 vec-3] in request order", batchIDs)
	}

	deleted, err := client.DeleteVectorsAlpha1(ctx, &runtimev1pb.DeleteVectorsRequestAlpha1{StoreName: vectorStore, Collection: collection, Ids: []string{"vec-1"}, Options: waitForCompletion})
	if err != nil {
		return fmt.Errorf("delete vectors: %w", err)
	}
	if err = completed("delete vectors", deleted.GetAck(), 0); err != nil {
		return err
	}
	got, err := client.GetVectorsAlpha1(ctx, &runtimev1pb.GetVectorsRequestAlpha1{StoreName: vectorStore, Collection: collection, Ids: []string{"vec-1", "vec-2"}})
	if err != nil {
		return fmt.Errorf("get vectors: %w", err)
	}
	ids := make([]string, 0, len(got.GetRecords()))
	for _, r := range got.GetRecords() {
		ids = append(ids, r.GetId())
	}
	if err = sameIDs("get vectors after delete", ids, []string{"vec-2"}); err != nil {
		return err
	}

	if _, err = client.DeleteCollectionAlpha1(ctx, &runtimev1pb.DeleteCollectionRequestAlpha1{StoreName: vectorStore, Collection: collection}); err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	_, err = client.GetCollectionAlpha1(ctx, &runtimev1pb.GetCollectionRequestAlpha1{StoreName: vectorStore, Collection: collection})
	return notFound("get deleted collection", err)
}

func completed(step string, ack runtimev1pb.IndexAck, failedItems int) error {
	if ack != runtimev1pb.IndexAck_INDEX_ACK_COMPLETED || failedItems != 0 {
		return fmt.Errorf("%s: got ack %s with %d failed items, want a completed write", step, ack, failedItems)
	}
	return nil
}

func sameIDs(step string, got, want []string) error {
	slices.Sort(got)
	if !slices.Equal(got, want) {
		return fmt.Errorf("%s: got ids %v, want %v", step, got, want)
	}
	return nil
}

func notFound(step string, err error) error {
	if status.Code(err) != codes.NotFound {
		return fmt.Errorf("%s: got %v, want NOT_FOUND", step, err)
	}
	return nil
}

func appRouter() http.Handler {
	router := chi.NewRouter()
	router.Use(utils.LoggerMiddleware)
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Post("/test/{scenario}/{runID}", testHandler)
	return router
}

func main() {
	client = utils.GetGRPCClient(0)
	log.Printf("Search and vector app - listening on http://:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
