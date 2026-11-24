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

package list

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	wfstate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.wfengine.state.list")

const (
	// defaultPageSize and maxPageSize bound the number of instances in one
	// ListInstances page.
	defaultPageSize = 1024
	maxPageSize     = 4096

	// maxStorePages is the maximum number of store pages that one
	// ListInstances call reads. When it read that many, the call returns the
	// instances it found and a continuation token.
	maxStorePages = 16

	// scanTimeout is the time after which one ListInstances call reads no new
	// store page. A deadline on the call can make this time shorter.
	scanTimeout = 15 * time.Second

	// maxReadChunk is the maximum number of metadata rows in one BulkGet.
	maxReadChunk = 1024

	// bulkGetParallelism bounds the BulkGet of a store without a native
	// BulkGet (Redis, Etcd), which otherwise reads every key at the same time.
	bulkGetParallelism = 32

	// deriveConcurrency bounds the full loads of instances whose metadata row
	// has no summary.
	deriveConcurrency = 16

	// partitionKeyMetadata is the request metadata key that the actor state
	// store reads to find the partition of a key.
	partitionKeyMetadata = "partitionKey"

	tokenVersion = 1
)

// DeriveFunc loads the full state of an instance whose metadata row has no
// summary, and returns the summary derived from its history. It returns a nil
// summary and a nil error when the list must skip the instance. An error
// fails the ListInstances call. It must not write to the store.
type DeriveFunc func(ctx context.Context, instanceID string) (*backend.BackendWorkflowSummary, error)

type ListInstancesOptions struct {
	ComponentStore *compstore.ComponentStore
	Namespace      string
	AppID          string
	Request        *protos.ListInstancesRequest
	Derive         DeriveFunc
}

// ListInstances lists the workflow instances that match the filter of the
// request, with their summaries. It reads the summary from the metadata row
// of each instance. It never writes.
//
// The call reads the metadata keys in store pages. It asks the store for at
// most as many keys as the page still has room for. Every key can match, so
// a store page never gives more instances than the page can hold. The call
// thus never stops inside a store page: its continuation token holds the
// token of the store, and the next call starts at the next store page. An
// instance that is deleted or purged between two calls does not move that
// start.
//
// The call always reads its first store page, so every call makes progress.
// Before each further store page, it checks its budget: at most maxStorePages
// store pages, and no new store page after scanTimeout or after half of the
// time that is left before the deadline of ctx. A page can thus be short or
// empty while its continuation token is not nil. The budget does not apply
// inside a store page, so a caller with a short deadline sets a small page
// size: the first store page has no more keys than the page size. The order
// of the instances is the order of the store keys.
//
// Errors:
//   - An invalid continuation token gives InvalidArgument.
//   - A full load that this daprd cannot verify (ConfigurationError) gives
//     FailedPrecondition.
//   - A failed store read gives Unavailable.
//   - When ctx ends, the call returns the error of ctx.
//
// A failed call returns no token. The client calls again with the same token
// and loses no instance.
func ListInstances(ctx context.Context, opts ListInstancesOptions) (*protos.ListInstancesResponse, error) {
	store, ks, err := actorStore(opts.ComponentStore)
	if err != nil {
		return nil, err
	}

	req := opts.Request
	storeToken, err := decodeToken(req.GetContinuationToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	pageSize := defaultPageSize
	if req.PageSize != nil && req.GetPageSize() > 0 {
		pageSize = min(int(req.GetPageSize()), maxPageSize)
	}

	// Stop reading new store pages at half of the caller's remaining time, so
	// that the call can return its progress before the caller gives up. A
	// call that ends at the caller's deadline loses its progress, and a retry
	// with the same token does the same work again.
	now := time.Now()
	deadline := now.Add(scanTimeout)
	if d, ok := ctx.Deadline(); ok {
		if half := now.Add(d.Sub(now) / 2); half.Before(deadline) {
			deadline = half
		}
	}

	w := &walker{
		store:  store,
		derive: opts.Derive,
		filter: req.GetFilter(),
		now:    now,
	}
	sc := scope{
		namespace: opts.Namespace,
		appID:     opts.AppID,
		idPrefix:  req.GetInstanceIdPrefix(),
	}

	for pages := 0; ; pages++ {
		if pages > 0 && (pages >= maxStorePages || !time.Now().Before(deadline)) {
			return w.response(storeToken)
		}

		//nolint:gosec // pageSize is at most maxPageSize, and out is shorter.
		limit := uint32(pageSize - len(w.out))
		page, err := keysPage(ctx, ks, sc, storeToken, &limit)
		if err != nil {
			return nil, readError(ctx, fmt.Errorf("failed to list workflow metadata keys: %w", err))
		}

		if err = w.scan(ctx, page.entries); err != nil {
			return nil, err
		}

		if page.token == nil {
			return w.response(nil)
		}
		storeToken = page.token
		if len(w.out) >= pageSize {
			return w.response(storeToken)
		}
	}
}

// walker collects the matching instances of one ListInstances call.
type walker struct {
	store  state.Store
	derive DeriveFunc
	filter *protos.ListInstancesFilter
	// now is the time of the call. The duration filter uses it for the
	// instances that did not complete.
	now time.Time

	out []*protos.WorkflowInstanceSummary
}

// scan reads the summaries of the instances of entries, and keeps those that
// match the filter.
func (w *walker) scan(ctx context.Context, entries []keyEntry) error {
	for len(entries) > 0 {
		n := min(len(entries), maxReadChunk)
		chunk := entries[:n]
		entries = entries[n:]

		sums, err := w.read(ctx, chunk)
		if err != nil {
			return err
		}

		for i, sum := range sums {
			if sum != nil && wfstate.MatchesFilter(sum, w.filter, w.now) {
				w.out = append(w.out, instanceSummary(chunk[i].id, sum))
			}
		}
	}
	return nil
}

// read returns the summaries of the instances in chunk. A nil summary means
// that the list skips the instance: it was deleted or purged after its key
// was listed, or its state cannot be read for a reason that a retry cannot
// change. A row without a summary, and a row that the store did not return,
// are derived from a full load. Any other failed read fails the call.
func (w *walker) read(ctx context.Context, chunk []keyEntry) ([]*backend.BackendWorkflowSummary, error) {
	reqs := make([]state.GetRequest, len(chunk))
	for i, e := range chunk {
		reqs[i] = state.GetRequest{
			Key: e.key,
			// The same partition key as an actor state read of this row.
			Metadata: map[string]string{partitionKeyMetadata: strings.TrimSuffix(e.key, "||"+wfstate.MetadataKey)},
		}
	}

	resps, err := w.store.BulkGet(ctx, reqs, state.BulkGetOpts{Parallelism: bulkGetParallelism})
	if err != nil {
		return nil, readError(ctx, fmt.Errorf("failed to read workflow metadata: %w", err))
	}
	byKey := make(map[string]*state.BulkGetResponse, len(resps))
	for i := range resps {
		byKey[resps[i].Key] = &resps[i]
	}

	sums := make([]*backend.BackendWorkflowSummary, len(chunk))
	var derive []int
	for i, e := range chunk {
		r, ok := byKey[e.key]
		if !ok || r.Error != "" {
			// The full load reads the row again, through the resiliency
			// policy of the actor state store, and finds out if the
			// instance still exists.
			derive = append(derive, i)
			continue
		}
		if len(r.Data) == 0 {
			// Deleted or purged after the key was listed. A load also
			// reports no state for an empty metadata row.
			continue
		}
		var meta backend.BackendWorkflowStateMetadata
		if proto.Unmarshal(r.Data, &meta) != nil || meta.GetSummary() == nil {
			// Written by a daprd without summaries, or in the legacy JSON
			// format.
			derive = append(derive, i)
			continue
		}
		sums[i] = meta.GetSummary()
	}

	if len(derive) > 0 {
		if err := w.deriveAll(ctx, chunk, derive, sums); err != nil {
			return nil, err
		}
	}

	return sums, nil
}

// deriveAll derives the summaries of the instances at the indexes in derive
// from their full state. An instance that the list skips keeps a nil
// summary. The first failed load stops the other loads and fails the call.
func (w *walker) deriveAll(ctx context.Context, chunk []keyEntry, derive []int, sums []*backend.BackendWorkflowSummary) error {
	if w.derive == nil {
		return status.Error(codes.Internal, "cannot list workflow instances without a summary: no full load is configured")
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(deriveConcurrency)
	for _, i := range derive {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			sum, err := w.derive(gctx, chunk[i].id)
			if err != nil {
				return fmt.Errorf("failed to load workflow instance '%s': %w", chunk[i].id, err)
			}
			sums[i] = sum
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		if ctx.Err() == nil {
			log.Warnf("Failed to list workflow instances: %s", err)
		}
		return readError(ctx, err)
	}
	return nil
}

// readError returns the error of a call whose read failed with err. When the
// call ended, it is the error of ctx. When this daprd cannot verify the
// history of an instance (ConfigurationError), the error has the code
// FailedPrecondition: a retry fails the same way until the configuration
// changes. Otherwise the error has the code Unavailable.
func readError(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	var cfgErr *wferrors.ConfigurationError
	if errors.As(err, &cfgErr) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Unavailable, err.Error())
}

func (w *walker) response(storeToken *string) (*protos.ListInstancesResponse, error) {
	resp := &protos.ListInstancesResponse{Instances: w.out}
	if storeToken != nil {
		tok, err := encodeToken(*storeToken)
		if err != nil {
			return nil, err
		}
		resp.ContinuationToken = &tok
	}
	return resp, nil
}

func instanceSummary(id string, sum *backend.BackendWorkflowSummary) *protos.WorkflowInstanceSummary {
	return &protos.WorkflowInstanceSummary{
		InstanceId:           id,
		Name:                 sum.GetName(),
		RuntimeStatus:        sum.GetRuntimeStatus(),
		CreatedTimestamp:     sum.GetCreatedAt(),
		StartedAt:            sum.GetStartedAt(),
		LastUpdatedTimestamp: sum.GetLastUpdatedAt(),
		CompletedTimestamp:   sum.GetCompletedAt(),
		Version:              sum.Version,
		ParentInstanceId:     sum.GetParentInstanceId(),
		ParentAppId:          sum.ParentAppId,
	}
}

// listToken is the decoded ListInstances continuation token. It holds the
// token of the store page where the next call starts. The version marks the
// format. A token from ListInstanceIDs is the token of the store, without
// this wrapper, and does not decode.
type listToken struct {
	Version    int     `json:"v"`
	StoreToken *string `json:"t"`
}

func encodeToken(storeToken string) (string, error) {
	b, err := json.Marshal(listToken{Version: tokenVersion, StoreToken: &storeToken})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeToken returns the store token in s. An empty s gives a nil store
// token: the list starts at the first store page.
func decodeToken(s string) (*string, error) {
	if s == "" {
		return nil, nil
	}
	errInvalid := errors.New("invalid continuation token: not a token from ListInstances; tokens from ListInstanceIDs are not valid for ListInstances")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var t listToken
	if err = dec.Decode(&t); err != nil || dec.More() {
		return nil, errInvalid
	}
	if t.Version != tokenVersion || t.StoreToken == nil {
		return nil, errors.New("invalid continuation token: unknown format")
	}
	return t.StoreToken, nil
}
