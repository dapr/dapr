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
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/ptr"
)

const (
	testAppID     = "app"
	testNamespace = "default"
)

// fakeStore is an in-memory state store with KeysLike and BulkGet. KeysLike
// matches a pattern as SQLite matches LIKE: "%" matches any run of
// characters, "_" matches one character, and ASCII letters match without
// case. It pages by key: the token is the last key of the page, and the next
// page starts after it, as the SQL stores page by row ID or by key. A delete
// between two pages does not move the next page. The store counts every
// write.
type fakeStore struct {
	mu   sync.Mutex
	keys []string
	data map[string][]byte

	writes        atomic.Int64
	keysLikeCalls atomic.Int64
	bulkGetCalls  atomic.Int64
	rowsRead      atomic.Int64
	badBulkOpts   atomic.Int64
	badPartition  atomic.Int64
	// maxKeysLikePage is the largest page size of a KeysLike request.
	maxKeysLikePage atomic.Int64

	// getErr, when set, returns the error of a Get of key. Tests set it
	// before the first read.
	getErr func(key string) error
	// bulkItemErr, when set, returns the error that BulkGet reports for
	// key, as a native BulkGet reports a row that it failed to read. Tests
	// set it before the first read.
	bulkItemErr func(key string) string
}

func newFakeStore() *fakeStore {
	return &fakeStore{data: make(map[string][]byte)}
}

func (f *fakeStore) put(key string, value []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[key]; !ok {
		i, _ := slices.BinarySearch(f.keys, key)
		f.keys = slices.Insert(f.keys, i, key)
	}
	f.data[key] = value
}

// remove deletes the rows of an instance, as a purge does.
func (f *fakeStore) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = slices.DeleteFunc(f.keys, func(k string) bool {
		if strings.Contains(k, "||"+id+"||") {
			delete(f.data, k)
			return true
		}
		return false
	})
}

// addGhostKey lists key without a value, as a row deleted after KeysLike.
func (f *fakeStore) addGhostKey(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, _ := slices.BinarySearch(f.keys, key)
	f.keys = slices.Insert(f.keys, i, key)
}

func (f *fakeStore) Init(context.Context, state.Metadata) error { return nil }
func (f *fakeStore) Features() []state.Feature {
	return []state.Feature{state.FeatureETag, state.FeatureTransactional, state.FeatureKeysLike}
}
func (f *fakeStore) Close() error                               { return nil }
func (f *fakeStore) GetComponentMetadata() metadata.MetadataMap { return nil }

func (f *fakeStore) Get(_ context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsRead.Add(1)
	if f.getErr != nil {
		if err := f.getErr(req.Key); err != nil {
			return nil, err
		}
	}
	v, ok := f.data[req.Key]
	if !ok {
		return &state.GetResponse{}, nil
	}
	return &state.GetResponse{Data: v, ETag: ptr.Of("1")}, nil
}

func (f *fakeStore) BulkGet(_ context.Context, reqs []state.GetRequest, opts state.BulkGetOpts) ([]state.BulkGetResponse, error) {
	f.bulkGetCalls.Add(1)
	if opts.Parallelism <= 0 || opts.Parallelism > bulkGetParallelism {
		f.badBulkOpts.Add(1)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]state.BulkGetResponse, 0, len(reqs))
	for _, r := range reqs {
		f.rowsRead.Add(1)
		if r.Metadata["partitionKey"]+"||metadata" != r.Key {
			f.badPartition.Add(1)
		}
		if f.bulkItemErr != nil {
			if e := f.bulkItemErr(r.Key); e != "" {
				out = append(out, state.BulkGetResponse{Key: r.Key, Error: e})
				continue
			}
		}
		out = append(out, state.BulkGetResponse{Key: r.Key, Data: f.data[r.Key]})
	}
	// A native BulkGet can return the rows in any order.
	slices.Reverse(out)
	return out, nil
}

func (f *fakeStore) KeysLike(_ context.Context, req *state.KeysLikeRequest) (*state.KeysLikeResponse, error) {
	f.keysLikeCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()

	like, err := likeRegexp(req.Pattern)
	if err != nil {
		return nil, err
	}

	i := 0
	if req.ContinuationToken != nil {
		i, _ = slices.BinarySearch(f.keys, *req.ContinuationToken)
		if i < len(f.keys) && f.keys[i] == *req.ContinuationToken {
			i++
		}
	}
	limit := len(f.keys)
	if req.PageSize != nil && *req.PageSize > 0 {
		limit = int(*req.PageSize)
		for {
			m := f.maxKeysLikePage.Load()
			if int64(limit) <= m || f.maxKeysLikePage.CompareAndSwap(m, int64(limit)) {
				break
			}
		}
	}

	// Most keys are not metadata rows. A check of the literal end of the
	// pattern rejects them before the slower regular expression.
	suffix := req.Pattern[strings.LastIndexAny(req.Pattern, `%_\`)+1:]

	resp := &state.KeysLikeResponse{Keys: []string{}}
	for ; i < len(f.keys) && len(resp.Keys) < limit; i++ {
		k := f.keys[i]
		if len(k) < len(suffix) || !strings.EqualFold(k[len(k)-len(suffix):], suffix) {
			continue
		}
		if like.MatchString(k) {
			resp.Keys = append(resp.Keys, f.keys[i])
		}
	}
	if len(resp.Keys) == limit && i < len(f.keys) {
		resp.ContinuationToken = ptr.Of(resp.Keys[len(resp.Keys)-1])
	}
	return resp, nil
}

func (f *fakeStore) Set(context.Context, *state.SetRequest) error {
	f.writes.Add(1)
	return errors.New("write not allowed")
}

func (f *fakeStore) Delete(context.Context, *state.DeleteRequest) error {
	f.writes.Add(1)
	return errors.New("write not allowed")
}

func (f *fakeStore) BulkSet(context.Context, []state.SetRequest, state.BulkStoreOpts) error {
	f.writes.Add(1)
	return errors.New("write not allowed")
}

func (f *fakeStore) BulkDelete(context.Context, []state.DeleteRequest, state.BulkStoreOpts) error {
	f.writes.Add(1)
	return errors.New("write not allowed")
}

func (f *fakeStore) Multi(context.Context, *state.TransactionalStateRequest) error {
	f.writes.Add(1)
	return errors.New("write not allowed")
}

// likeRegexp compiles a LIKE pattern, with "\" as its escape character.
func likeRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`(?is)^`)
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '%':
			b.WriteString(`.*`)
		case '_':
			b.WriteString(`.`)
		case '\\':
			if i+1 < len(pattern) {
				i++
			}
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`$`)
	return regexp.Compile(b.String())
}

// metadataKey returns the key of the metadata row of a workflow instance.
func metadataKey(appID, namespace, id string) string {
	return appID + "||dapr.internal." + namespace + "." + appID + ".workflow||" + id + "||metadata"
}

func compStoreFor(t testing.TB, s *fakeStore) *compstore.ComponentStore {
	t.Helper()
	cs := compstore.New()
	require.NoError(t, cs.AddStateStoreActor("fake", s))
	return cs
}

// listAllIDs calls ListInstanceIDs until the token is nil.
func listAllIDs(t *testing.T, opts ListOptions) []string {
	t.Helper()
	var ids []string
	for calls := 1; ; calls++ {
		require.Less(t, calls, 1000, "the list does not end")
		resp, err := ListInstanceIDs(t.Context(), opts)
		require.NoError(t, err)
		ids = append(ids, resp.Keys...)
		if resp.ContinuationToken == nil {
			return ids
		}
		opts.ContinuationToken = resp.ContinuationToken
	}
}

func TestListInstanceIDs(t *testing.T) {
	t.Parallel()

	t.Run("pages and the prefix filter", func(t *testing.T) {
		t.Parallel()

		s := newFakeStore()
		for _, id := range []string{"inst-0", "inst-1", "inst-2", "inst-3", "inst-4", "instAx", "inst_x"} {
			s.put(metadataKey(testAppID, testNamespace, id), []byte("m"))
			s.put(strings.TrimSuffix(metadataKey(testAppID, testNamespace, id), "metadata")+"history-000000", []byte("h"))
		}
		cs := compStoreFor(t, s)

		resp, err := ListInstanceIDs(t.Context(), ListOptions{
			ComponentStore: cs, Namespace: testNamespace, AppID: testAppID, PageSize: ptr.Of(uint32(3)),
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"inst-0", "inst-1", "inst-2"}, resp.Keys)
		require.NotNil(t, resp.ContinuationToken)

		resp, err = ListInstanceIDs(t.Context(), ListOptions{
			ComponentStore: cs, Namespace: testNamespace, AppID: testAppID, ContinuationToken: resp.ContinuationToken,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"inst-3", "inst-4", "instAx", "inst_x"}, resp.Keys)
		assert.Nil(t, resp.ContinuationToken)

		// The pattern stops at "_"; the prefix filter drops "instAx".
		assert.Equal(t, []string{"inst_x"}, listAllIDs(t, ListOptions{
			ComponentStore: cs, Namespace: testNamespace, AppID: testAppID, IDPrefix: "inst_",
		}))

		// An empty page is an empty list.
		resp, err = ListInstanceIDs(t.Context(), ListOptions{
			ComponentStore: cs, Namespace: testNamespace, AppID: testAppID, IDPrefix: "nothing",
		})
		require.NoError(t, err)
		assert.NotNil(t, resp.Keys)
		assert.Empty(t, resp.Keys)
	})

	// Apps that share one store list only their own instances, though the
	// LIKE pattern of one matches the keys of the other.
	t.Run("apps that share a store", func(t *testing.T) {
		t.Parallel()

		s := newFakeStore()
		apps := []struct{ appID, namespace string }{
			{"a_b", testNamespace},
			{"axb", testNamespace},
			{"A_B", testNamespace},
			{testAppID, "n_s"},
			{testAppID, "nxs"},
		}
		want := make(map[string][]string)
		for i, a := range apps {
			for j := range 3 {
				id := a.appID + "-" + a.namespace + "-" + string(rune('a'+j))
				s.put(metadataKey(a.appID, a.namespace, id), []byte("m"))
				want[a.appID+"/"+a.namespace] = append(want[a.appID+"/"+a.namespace], id)
			}
			// An activity row of the app.
			s.put(a.appID+"||dapr.internal."+a.namespace+"."+a.appID+".activity||act-"+string(rune('0'+i))+"||metadata", []byte("m"))
		}
		cs := compStoreFor(t, s)

		for _, a := range apps {
			t.Run(a.appID+"/"+a.namespace, func(t *testing.T) {
				w := want[a.appID+"/"+a.namespace]
				for _, pageSize := range []*uint32{nil, ptr.Of(uint32(1)), ptr.Of(uint32(2))} {
					got := listAllIDs(t, ListOptions{
						ComponentStore: cs, Namespace: a.namespace, AppID: a.appID, PageSize: pageSize,
					})
					assert.ElementsMatch(t, w, got)
				}
				got := listAllIDs(t, ListOptions{
					ComponentStore: cs, Namespace: a.namespace, AppID: a.appID, IDPrefix: w[1],
				})
				assert.Equal(t, w[1:2], got)
			})
		}
	})
}
