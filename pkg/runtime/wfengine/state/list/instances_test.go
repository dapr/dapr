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
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	wfstate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

const testActorType = "dapr.internal." + testNamespace + "." + testAppID + ".workflow"

// actorState reads the fake store as the actor state store does.
type actorState struct{ s *fakeStore }

func (a actorState) Get(ctx context.Context, req *api.GetStateRequest, _ bool) (*api.StateResponse, error) {
	r, err := a.s.Get(ctx, &state.GetRequest{Key: testAppID + "||" + req.ActorKey() + "||" + req.Key})
	if err != nil {
		return nil, err
	}
	return &api.StateResponse{Data: r.Data, ETag: r.ETag}, nil
}

func (a actorState) GetBulk(ctx context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
	out := make(api.BulkStateResponse, len(req.Keys))
	for _, k := range req.Keys {
		r, err := a.s.Get(ctx, &state.GetRequest{Key: testAppID + "||" + req.ActorKey() + "||" + k})
		if err != nil {
			return nil, err
		}
		out[k] = api.BulkStateEntry{Data: r.Data, ETag: r.ETag}
	}
	return out, nil
}

func (a actorState) TransactionalStateOperation(context.Context, bool, *api.TransactionalRequest, bool) error {
	a.s.writes.Add(1)
	return errors.New("write not allowed")
}

var testStateOpts = wfstate.Options{
	AppID:             testAppID,
	Namespace:         testNamespace,
	WorkflowActorType: testActorType,
	ActivityActorType: "dapr.internal." + testNamespace + "." + testAppID + ".activity",
}

// loadDerive is the DeriveFunc of the backend: a full load of the instance.
func loadDerive(s *fakeStore) DeriveFunc {
	return func(ctx context.Context, id string) (*backend.BackendWorkflowSummary, error) {
		return wfstate.LoadSummary(ctx, actorState{s}, id, testStateOpts)
	}
}

// rowKey returns the store key of the row of instance id.
func rowKey(id, key string) string {
	return testAppID + "||" + testActorType + "||" + id + "||" + key
}

func ts(sec int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 11, 20, 10, 0, sec, 0, time.UTC))
}

type instanceKind int

const (
	kindRunning instanceKind = iota
	kindCompleted
	kindFailed
	kindSuspended
	kindPending
)

// historyFor returns the history and inbox of an instance of kind, with the
// workflow name and created time given. A completed instance completes 3
// seconds after it was created.
func historyFor(kind instanceKind, name string, created int, extra int) (history, inbox []*backend.HistoryEvent) {
	start := &backend.HistoryEvent{EventId: -1, Timestamp: ts(created), EventType: &protos.HistoryEvent_ExecutionStarted{
		ExecutionStarted: &protos.ExecutionStartedEvent{Name: name},
	}}
	if kind == kindPending {
		return nil, []*backend.HistoryEvent{start}
	}
	history = []*backend.HistoryEvent{
		{EventId: -1, Timestamp: ts(created + 1), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}}},
		start,
	}
	for i := range extra {
		history = append(history, &backend.HistoryEvent{EventId: int32(i), Timestamp: ts(created + 2), EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act", Input: nil},
		}})
	}
	switch kind {
	case kindCompleted, kindFailed:
		st := protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED
		if kind == kindFailed {
			st = protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED
		}
		history = append(history, &backend.HistoryEvent{EventId: -1, Timestamp: ts(created + 3), EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{WorkflowStatus: st},
		}})
	case kindSuspended:
		history = append(history, &backend.HistoryEvent{EventId: -1, Timestamp: ts(created + 3), EventType: &protos.HistoryEvent_ExecutionSuspended{
			ExecutionSuspended: &protos.ExecutionSuspendedEvent{},
		}})
	}
	return history, inbox
}

// putInstance writes the rows of an instance as a save writes them. A legacy
// instance has a metadata row without a summary, as an older daprd writes it.
func putInstance(t testing.TB, s *fakeStore, id string, history, inbox []*backend.HistoryEvent, legacy bool) {
	t.Helper()
	ws := wfstate.NewState(testStateOpts)
	for _, e := range inbox {
		ws.AddToInbox(e)
	}
	for _, e := range history {
		ws.AddToHistory(e)
	}
	req, err := ws.GetSaveRequest(id)
	require.NoError(t, err)
	for _, op := range req.Operations {
		u, ok := op.Request.(api.TransactionalUpsert)
		require.True(t, ok)
		b, ok := u.Value.([]byte)
		require.True(t, ok)
		if u.Key == wfstate.MetadataKey && legacy {
			var meta backend.BackendWorkflowStateMetadata
			require.NoError(t, proto.Unmarshal(b, &meta))
			meta.Summary = nil
			b, err = proto.Marshal(&meta)
			require.NoError(t, err)
		}
		s.put(rowKey(id, u.Key), b)
	}
}

// editMetadata changes the metadata row of instance id.
func editMetadata(t *testing.T, s *fakeStore, id string, edit func(*backend.BackendWorkflowStateMetadata)) {
	t.Helper()
	key := rowKey(id, wfstate.MetadataKey)
	s.mu.Lock()
	var meta backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(s.data[key], &meta))
	s.mu.Unlock()
	edit(&meta)
	b, err := proto.Marshal(&meta)
	require.NoError(t, err)
	s.put(key, b)
}

// instID returns the ID of the i-th instance of newTestStore.
func instID(i int) string {
	return fmt.Sprintf("inst-%04d", i)
}

// instIDs returns the IDs of the instances of newTestStore at the indexes.
func instIDs(is ...int) []string {
	ids := make([]string, len(is))
	for n, i := range is {
		ids[n] = instID(i)
	}
	return ids
}

type testInstance struct {
	id      string
	kind    instanceKind
	legacy  bool
	history []*backend.HistoryEvent
	inbox   []*backend.HistoryEvent
}

// newTestStore writes n instances: their kind cycles through kinds, and every
// legacyEvery-th one is legacy (0: none).
func newTestStore(t testing.TB, n int, kinds []instanceKind, legacyEvery int) (*fakeStore, []testInstance) {
	t.Helper()
	s := newFakeStore()
	insts := make([]testInstance, n)
	for i := range n {
		kind := kinds[i%len(kinds)]
		h, in := historyFor(kind, "wf-"+strconv.Itoa(i%3), i, 2)
		insts[i] = testInstance{
			id:      instID(i),
			kind:    kind,
			legacy:  legacyEvery > 0 && i%legacyEvery == 0,
			history: h,
			inbox:   in,
		}
		putInstance(t, s, insts[i].id, h, in, insts[i].legacy)
	}
	return s, insts
}

func listOpts(cs *compstore.ComponentStore, derive DeriveFunc, req *protos.ListInstancesRequest) ListInstancesOptions {
	return ListInstancesOptions{
		ComponentStore: cs,
		Namespace:      testNamespace,
		AppID:          testAppID,
		Request:        req,
		Derive:         derive,
	}
}

type walkResult struct {
	pages [][]*protos.WorkflowInstanceSummary
	calls int
}

// walk calls ListInstances until the token is nil.
func walk(t *testing.T, cs *compstore.ComponentStore, derive DeriveFunc, req *protos.ListInstancesRequest) walkResult {
	t.Helper()
	var res walkResult
	for {
		res.calls++
		require.Less(t, res.calls, 10_000, "walk does not end")
		resp, err := ListInstances(t.Context(), listOpts(cs, derive, req))
		require.NoError(t, err)
		res.pages = append(res.pages, resp.GetInstances())
		if resp.ContinuationToken == nil {
			return res
		}
		req = proto.Clone(req).(*protos.ListInstancesRequest)
		req.ContinuationToken = resp.ContinuationToken
	}
}

func (w walkResult) ids() []string {
	ids := make([]string, 0, len(w.pages))
	for _, p := range w.pages {
		ids = append(ids, idsOfPage(p)...)
	}
	return ids
}

func idsOfPage(p []*protos.WorkflowInstanceSummary) []string {
	ids := make([]string, 0, len(p))
	for _, s := range p {
		ids = append(ids, s.GetInstanceId())
	}
	return ids
}

func idsOf(insts []testInstance, keep func(testInstance) bool) []string {
	var ids []string
	for _, in := range insts {
		if keep(in) {
			ids = append(ids, in.id)
		}
	}
	return ids
}

func all(testInstance) bool { return true }

var statusOf = map[instanceKind]protos.OrchestrationStatus{
	kindRunning:   protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING,
	kindCompleted: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
	kindFailed:    protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
	kindSuspended: protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED,
	kindPending:   protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING,
}

var allKinds = []instanceKind{kindRunning, kindCompleted, kindFailed, kindSuspended, kindPending}

var runningFilter = &protos.ListInstancesFilter{RuntimeStatus: []protos.OrchestrationStatus{
	protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING,
}}

func TestListInstances_Pages(t *testing.T) {
	t.Parallel()

	s, insts := newTestStore(t, 50, allKinds, 0)
	cs := compStoreFor(t, s)

	t.Run("all instances, page size 7", func(t *testing.T) {
		t.Parallel()
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(7))})
		for i, p := range res.pages {
			if i < len(res.pages)-1 {
				assert.Len(t, p, 7)
			} else {
				assert.LessOrEqual(t, len(p), 7)
			}
		}
		assert.Equal(t, idsOf(insts, all), res.ids())
	})

	t.Run("selective filter, page size 3", func(t *testing.T) {
		t.Parallel()
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{
			PageSize: ptr.Of(uint32(3)),
			Filter: &protos.ListInstancesFilter{RuntimeStatus: []protos.OrchestrationStatus{
				protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED,
			}},
		})
		for _, p := range res.pages {
			assert.LessOrEqual(t, len(p), 3)
		}
		assert.Equal(t, idsOf(insts, func(in testInstance) bool { return in.kind == kindSuspended }), res.ids())
	})

	t.Run("summaries equal the derivation from history", func(t *testing.T) {
		t.Parallel()
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{})
		require.Len(t, res.pages, 1)
		require.Len(t, res.pages[0], len(insts))
		for i, got := range res.pages[0] {
			want := wfstate.Summarize(insts[i].history, insts[i].inbox)
			assert.Equal(t, insts[i].id, got.GetInstanceId())
			assert.Equal(t, want.GetName(), got.GetName())
			assert.Equal(t, statusOf[insts[i].kind], got.GetRuntimeStatus())
			assert.True(t, proto.Equal(want.GetCreatedAt(), got.GetCreatedTimestamp()))
			assert.True(t, proto.Equal(want.GetStartedAt(), got.GetStartedAt()))
			assert.True(t, proto.Equal(want.GetLastUpdatedAt(), got.GetLastUpdatedTimestamp()))
			assert.True(t, proto.Equal(want.GetCompletedAt(), got.GetCompletedTimestamp()))
		}
	})

	t.Run("created range and prefix", func(t *testing.T) {
		t.Parallel()
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{
			InstanceIdPrefix: ptr.Of("inst-001"),
			Filter: &protos.ListInstancesFilter{
				CreatedTimeFrom: ts(12),
				CreatedTimeTo:   ts(19),
			},
		})
		// inst-0010..0019, created at second i.
		assert.Equal(t, instIDs(12, 13, 14, 15, 16, 17, 18), res.ids())
	})

	t.Run("completed range excludes instances without completedAt", func(t *testing.T) {
		t.Parallel()
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{
			Filter: &protos.ListInstancesFilter{CompletedTimeFrom: ts(0)},
		})
		assert.Equal(t, idsOf(insts, func(in testInstance) bool {
			return in.kind == kindCompleted || in.kind == kindFailed
		}), res.ids())
	})

	// The instances were created in 2026-11-20. A completed instance ran
	// for 3 seconds. An instance that did not complete runs until the time
	// of the call.
	t.Run("duration uses the time of the call for an instance that did not complete", func(t *testing.T) {
		t.Parallel()
		done := func(in testInstance) bool { return in.kind == kindCompleted || in.kind == kindFailed }
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{
			Filter: &protos.ListInstancesFilter{MinDuration: durationpb.New(time.Hour)},
		})
		assert.Equal(t, idsOf(insts, func(in testInstance) bool { return !done(in) }), res.ids())

		res = walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{
			Filter: &protos.ListInstancesFilter{MaxDuration: durationpb.New(time.Hour)},
		})
		assert.Equal(t, idsOf(insts, done), res.ids())

		res = walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{
			Filter: &protos.ListInstancesFilter{MinDuration: durationpb.New(3 * time.Second), MaxDuration: durationpb.New(4 * time.Second)},
		})
		assert.Equal(t, idsOf(insts, done), res.ids())
	})

	assert.Zero(t, s.writes.Load())
}

// Each store page asks for no more keys than the page has room for, so a
// page never overflows and a token never points inside a store page.
func TestListInstances_StorePagesAndBudget(t *testing.T) {
	t.Parallel()

	s, insts := newTestStore(t, 40, []instanceKind{kindCompleted}, 0)
	// Only the last two instances in key order match.
	for _, id := range instIDs(38, 39) {
		h, in := historyFor(kindRunning, "wf", 99, 0)
		putInstance(t, s, id, h, in, false)
	}
	cs := compStoreFor(t, s)

	t.Run("a budget stop returns an empty page with a token", func(t *testing.T) {
		s.keysLikeCalls.Store(0)
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(2)), Filter: runningFilter})
		assert.Equal(t, instIDs(38, 39), res.ids())
		// 38 keys that do not match, in store pages of 2. The first call
		// reads maxStorePages store pages and finds no match.
		require.Equal(t, 2, res.calls)
		assert.Empty(t, res.pages[0])
		assert.Equal(t, int64(maxStorePages+4), s.keysLikeCalls.Load())
	})

	t.Run("pages across store pages are exact and complete", func(t *testing.T) {
		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(3))})
		for i, p := range res.pages {
			if i < len(res.pages)-1 {
				assert.Len(t, p, 3)
			}
		}
		assert.Equal(t, idsOf(insts, all), res.ids())
	})

	t.Run("the page size can change between calls", func(t *testing.T) {
		first, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(3))}))
		require.NoError(t, err)
		require.Len(t, first.GetInstances(), 3)
		storeToken, err := decodeToken(first.GetContinuationToken())
		require.NoError(t, err)
		// The token is the store token: the last key of the store page.
		assert.Equal(t, rowKey("inst-0002", wfstate.MetadataKey), *storeToken)

		res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{
			PageSize: ptr.Of(uint32(20)), ContinuationToken: first.ContinuationToken,
		})
		assert.Equal(t, idsOf(insts, all), append(idsOfPage(first.GetInstances()), res.ids()...))
	})

	assert.Zero(t, s.writes.Load())
}

// A client that purges the instances that a page listed, and then asks for
// the next page, loses no instance: the token is the store token, which a
// purge does not move.
func TestListInstances_ListThenPurge(t *testing.T) {
	t.Parallel()

	for name, purge := range map[string]string{
		"the first listed instance": instID(0),
		"a middle listed instance":  instID(1),
		"the last listed instance":  instID(2),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, _ := newTestStore(t, 8, []instanceKind{kindCompleted}, 0)
			cs := compStoreFor(t, s)
			first, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(3))}))
			require.NoError(t, err)
			require.Equal(t, instIDs(0, 1, 2), idsOfPage(first.GetInstances()))

			s.remove(purge)
			second, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), &protos.ListInstancesRequest{
				PageSize: ptr.Of(uint32(3)), ContinuationToken: first.ContinuationToken,
			}))
			require.NoError(t, err)
			assert.Equal(t, instIDs(3, 4, 5), idsOfPage(second.GetInstances()))
		})
	}

	t.Run("list-and-purge loop", func(t *testing.T) {
		t.Parallel()
		s, insts := newTestStore(t, 10, []instanceKind{kindCompleted}, 0)
		cs := compStoreFor(t, s)
		req := &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(3))}
		var seen []string
		for calls := 0; ; calls++ {
			require.Less(t, calls, 100, "walk does not end")
			resp, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), req))
			require.NoError(t, err)
			for _, in := range resp.GetInstances() {
				seen = append(seen, in.GetInstanceId())
				s.remove(in.GetInstanceId())
			}
			if resp.ContinuationToken == nil {
				break
			}
			req = proto.Clone(req).(*protos.ListInstancesRequest)
			req.ContinuationToken = resp.ContinuationToken
		}
		assert.Equal(t, idsOf(insts, all), seen)
		s.mu.Lock()
		defer s.mu.Unlock()
		assert.Empty(t, s.keys, "instances that were never listed, so never purged")
	})
}

func TestListInstances_LegacyRows(t *testing.T) {
	t.Parallel()

	s, insts := newTestStore(t, 30, allKinds, 3)
	cs := compStoreFor(t, s)

	before := make(map[string][]byte)
	s.mu.Lock()
	for k, v := range s.data {
		before[k] = slices.Clone(v)
	}
	s.mu.Unlock()

	var derived atomic.Int64
	derive := func(ctx context.Context, id string) (*backend.BackendWorkflowSummary, error) {
		derived.Add(1)
		return loadDerive(s)(ctx, id)
	}

	res := walk(t, cs, derive, &protos.ListInstancesRequest{})
	require.Len(t, res.pages, 1)
	require.Len(t, res.pages[0], len(insts))
	for i, got := range res.pages[0] {
		assert.Equal(t, insts[i].id, got.GetInstanceId())
		assert.Equal(t, statusOf[insts[i].kind], got.GetRuntimeStatus(), insts[i].id)
		want := wfstate.Summarize(insts[i].history, insts[i].inbox)
		assert.True(t, proto.Equal(want.GetCreatedAt(), got.GetCreatedTimestamp()))
	}
	assert.Equal(t, int64(10), derived.Load(), "only the legacy rows are loaded")

	// Nothing is written: the legacy rows keep no summary.
	assert.Zero(t, s.writes.Load())
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Equal(t, before, s.data)
}

func TestListInstances_SkipsVanishedInstances(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t, 6, []instanceKind{kindRunning}, 2)
	// A key listed without a row: deleted after KeysLike.
	s.addGhostKey(rowKey("inst-0003a", wfstate.MetadataKey))
	// A key listed without a row, which BulkGet fails to read: the full load
	// finds no state.
	s.addGhostKey(rowKey("inst-0003b", wfstate.MetadataKey))
	// inst-0005 has a summary, but BulkGet fails to read its row: the full
	// load reads it again.
	s.bulkItemErr = func(key string) string {
		if strings.Contains(key, "||inst-0003b||") || strings.Contains(key, "||inst-0005||") {
			return "row read failed"
		}
		return ""
	}
	cs := compStoreFor(t, s)

	var derived []string
	var mu sync.Mutex
	derive := func(ctx context.Context, id string) (*backend.BackendWorkflowSummary, error) {
		mu.Lock()
		derived = append(derived, id)
		mu.Unlock()
		if id == "inst-0002" {
			// Purged after its row was read, before its full load.
			s.remove(id)
		}
		return loadDerive(s)(ctx, id)
	}

	res := walk(t, cs, derive, &protos.ListInstancesRequest{})
	assert.Equal(t, instIDs(0, 1, 3, 4, 5), res.ids())
	assert.ElementsMatch(t, []string{instID(0), instID(2), "inst-0003b", instID(4), instID(5)}, derived,
		"an empty row is not loaded")
	for _, p := range res.pages {
		for _, in := range p {
			assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, in.GetRuntimeStatus(), in.GetInstanceId())
		}
	}
}

// An old instance whose state cannot be read, for a reason that a retry
// cannot change, is skipped. The walk does not stop at it.
func TestListInstances_SkipsUnreadableInstances(t *testing.T) {
	t.Parallel()

	tests := map[string]func(t *testing.T, s *fakeStore){
		"a history row that the metadata declares is missing": func(t *testing.T, s *fakeStore) {
			s.mu.Lock()
			defer s.mu.Unlock()
			k := rowKey("inst-0002", "history-000001")
			delete(s.data, k)
			s.keys = slices.DeleteFunc(s.keys, func(x string) bool { return x == k })
		},
		"a custom status row that does not decode": func(t *testing.T, s *fakeStore) {
			s.put(rowKey("inst-0002", "customStatus"), []byte{0xff})
		},
		"a history row that does not decode": func(t *testing.T, s *fakeStore) {
			s.put(rowKey("inst-0002", "history-000001"), []byte{0xff, 0xff, 0xff})
		},
		"a signature row that does not decode": func(t *testing.T, s *fakeStore) {
			editMetadata(t, s, "inst-0002", func(m *backend.BackendWorkflowStateMetadata) { m.SignatureLength = 1 })
			s.put(rowKey("inst-0002", "signature-000000"), []byte{0xff, 0xff, 0xff})
		},
		"a propagated history row that does not decode": func(t *testing.T, s *fakeStore) {
			s.put(rowKey("inst-0002", "propagated-history"), []byte{0xff, 0xff, 0xff})
		},
		// The metadata declares a signature that the store does not have, as
		// a changed row does. The load returns the state with a
		// VerificationError. GetInstance reports the error, and the list
		// does not show the instance.
		"a state that fails verification": func(t *testing.T, s *fakeStore) {
			editMetadata(t, s, "inst-0002", func(m *backend.BackendWorkflowStateMetadata) { m.SignatureLength = 1 })
		},
		"metadata over the entry limit": func(t *testing.T, s *fakeStore) {
			editMetadata(t, s, "inst-0002", func(m *backend.BackendWorkflowStateMetadata) { m.HistoryLength = 1 << 40 })
		},
	}
	for name, damage := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// All rows are legacy, so the list loads each instance in full.
			s, _ := newTestStore(t, 6, []instanceKind{kindCompleted}, 1)
			damage(t, s)
			cs := compStoreFor(t, s)
			res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(2))})
			assert.Equal(t, instIDs(0, 1, 3, 4, 5), res.ids())
			assert.Zero(t, s.writes.Load())
		})
	}

	t.Run("the damaged signature row loads with a VerificationError", func(t *testing.T) {
		t.Parallel()
		s, _ := newTestStore(t, 3, []instanceKind{kindCompleted}, 1)
		editMetadata(t, s, "inst-0001", func(m *backend.BackendWorkflowStateMetadata) { m.SignatureLength = 1 })
		_, err := wfstate.LoadWorkflowState(t.Context(), actorState{s}, "inst-0001", testStateOpts)
		var verr *wferrors.VerificationError
		require.ErrorAs(t, err, &verr)
	})
}

func TestListInstances_FailedLoadFailsTheCall(t *testing.T) {
	t.Parallel()

	// historyErr makes the reads of the history rows of instance id fail
	// while fail is true.
	historyErr := func(id string, fail *atomic.Bool) func(string) error {
		return func(key string) error {
			if fail.Load() && strings.Contains(key, "||"+id+"||history") {
				return errors.New("store unavailable")
			}
			return nil
		}
	}

	t.Run("a retry with the same token loses no instance", func(t *testing.T) {
		t.Parallel()

		s, insts := newTestStore(t, 9, allKinds, 3)
		var fail atomic.Bool
		fail.Store(true)
		// inst-0003 is legacy, so the list loads it in full.
		s.getErr = historyErr("inst-0003", &fail)
		cs := compStoreFor(t, s)

		req := &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(2))}
		var ids []string
		var failures int
		for calls := 0; ; calls++ {
			require.Less(t, calls, 100, "walk does not end")
			resp, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), req))
			if err != nil {
				failures++
				assert.Nil(t, resp)
				assert.Equal(t, codes.Unavailable, status.Code(err))
				assert.Contains(t, err.Error(), "inst-0003")
				// The store recovers, and the client calls again with the
				// same token.
				fail.Store(false)
				continue
			}
			ids = append(ids, idsOfPage(resp.GetInstances())...)
			if resp.ContinuationToken == nil {
				break
			}
			req = proto.Clone(req).(*protos.ListInstancesRequest)
			req.ContinuationToken = resp.ContinuationToken
		}
		assert.Equal(t, 1, failures)
		assert.Equal(t, idsOf(insts, all), ids)
	})

	t.Run("a failed read of the row and of the full load fails the call", func(t *testing.T) {
		t.Parallel()

		s, _ := newTestStore(t, 4, []instanceKind{kindCompleted}, 0)
		var fail atomic.Bool
		fail.Store(true)
		s.getErr = historyErr("inst-0001", &fail)
		s.bulkItemErr = func(key string) string {
			if strings.Contains(key, "||inst-0001||") {
				return "row read failed"
			}
			return ""
		}
		cs := compStoreFor(t, s)

		_, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), &protos.ListInstancesRequest{}))
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
		assert.Contains(t, err.Error(), "inst-0001")
	})

	// The history of inst-0001 is signed, and this daprd has no signer. The
	// state is intact, but this daprd cannot verify it.
	t.Run("a configuration error fails the call with FailedPrecondition", func(t *testing.T) {
		t.Parallel()

		s, _ := newTestStore(t, 3, []instanceKind{kindCompleted}, 1)
		editMetadata(t, s, "inst-0001", func(m *backend.BackendWorkflowStateMetadata) { m.SignatureLength = 1 })
		sig, err := proto.Marshal(&backend.HistorySignature{EventCount: 1})
		require.NoError(t, err)
		s.put(rowKey("inst-0001", "signature-000000"), sig)
		_, err = wfstate.LoadWorkflowState(t.Context(), actorState{s}, "inst-0001", testStateOpts)
		var cerr *wferrors.ConfigurationError
		require.ErrorAs(t, err, &cerr)

		cs := compStoreFor(t, s)
		resp, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), &protos.ListInstancesRequest{}))
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
		assert.Contains(t, err.Error(), "inst-0001")
	})

	t.Run("a failed key read fails the call", func(t *testing.T) {
		t.Parallel()

		s, _ := newTestStore(t, 2, []instanceKind{kindCompleted}, 0)
		cs := compstore.New()
		require.NoError(t, cs.AddStateStoreActor("fake", failingKeys{s}))
		_, err := ListInstances(t.Context(), listOpts(cs, loadDerive(s), &protos.ListInstancesRequest{}))
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	})

	t.Run("a cancelled call fails with the context error", func(t *testing.T) {
		t.Parallel()

		s, _ := newTestStore(t, 6, []instanceKind{kindRunning}, 2)
		cs := compStoreFor(t, s)
		ctx, cancel := context.WithCancel(t.Context())
		_, err := ListInstances(ctx, listOpts(cs, func(context.Context, string) (*backend.BackendWorkflowSummary, error) {
			cancel()
			return nil, context.Canceled
		}, &protos.ListInstancesRequest{}))
		require.ErrorIs(t, err, context.Canceled)
	})
}

// failingKeys is a store whose KeysLike fails.
type failingKeys struct{ *fakeStore }

func (failingKeys) KeysLike(context.Context, *state.KeysLikeRequest) (*state.KeysLikeResponse, error) {
	return nil, errors.New("store unavailable")
}

// One store page whose full loads take longer than half of the deadline of
// the client. A call does not stop inside a store page, so it reads its
// first store page whole. Each store page has no more keys than the
// requested page size, so a client with a short deadline sets a small page
// size, and each call returns its progress and a token before the deadline.
func TestListInstances_SlowPageReturnsProgress(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t, 64, []instanceKind{kindCompleted}, 1) // all legacy
	cs := compStoreFor(t, s)
	slow := func(ctx context.Context, id string) (*backend.BackendWorkflowSummary, error) {
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return loadDerive(s)(ctx, id)
	}

	req := &protos.ListInstancesRequest{PageSize: ptr.Of(uint32(1)), Filter: runningFilter}
	tokens := make([]string, 0, 3)
	for range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
		resp, err := ListInstances(ctx, listOpts(cs, slow, req))
		cerr := ctx.Err()
		cancel()
		require.NoError(t, err, "a call under a deadline returns its progress and a token")
		require.NoError(t, cerr, "the call returns before the deadline")
		assert.Empty(t, resp.GetInstances())
		require.NotNil(t, resp.ContinuationToken)
		tokens = append(tokens, resp.GetContinuationToken())
		req = proto.Clone(req).(*protos.ListInstancesRequest)
		req.ContinuationToken = resp.ContinuationToken
	}
	assert.Len(t, slices.Compact(slices.Clone(tokens)), len(tokens), "each call makes progress")
	assert.Equal(t, int64(1), s.maxKeysLikePage.Load(), "no store page has more keys than the page size")
}

func TestListInstances_LongNames(t *testing.T) {
	t.Parallel()

	// Two names that have the same first 256 bytes.
	prefix := strings.Repeat("n", 256)
	names := []string{prefix + "-first", prefix + "-second"}
	s := newFakeStore()
	for i, name := range names {
		h, in := historyFor(kindRunning, name, i, 0)
		putInstance(t, s, fmt.Sprintf("inst-%04d", i), h, in, false)
	}
	cs := compStoreFor(t, s)

	res := walk(t, cs, loadDerive(s), &protos.ListInstancesRequest{})
	require.Len(t, res.pages, 1)
	require.Len(t, res.pages[0], 2)
	assert.Equal(t, names[0], res.pages[0][0].GetName())
	assert.Equal(t, names[1], res.pages[0][1].GetName())
}

func TestListInstances_BoundedParallelism(t *testing.T) {
	t.Parallel()

	s, insts := newTestStore(t, 64, []instanceKind{kindCompleted}, 1)
	cs := compStoreFor(t, s)

	var cur, peak atomic.Int64
	derive := func(ctx context.Context, id string) (*backend.BackendWorkflowSummary, error) {
		n := cur.Add(1)
		defer cur.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		return loadDerive(s)(ctx, id)
	}

	res := walk(t, cs, derive, &protos.ListInstancesRequest{})
	assert.Len(t, res.ids(), len(insts))
	assert.LessOrEqual(t, peak.Load(), int64(deriveConcurrency))
	assert.Greater(t, peak.Load(), int64(1))
	assert.Zero(t, s.badBulkOpts.Load(), "every BulkGet must set a bounded parallelism")
	assert.Zero(t, s.badPartition.Load(), "every row read must carry its partition key")
}

func TestListInstances_InvalidToken(t *testing.T) {
	t.Parallel()

	s, _ := newTestStore(t, 2, []instanceKind{kindRunning}, 0)
	cs := compStoreFor(t, s)

	for name, tok := range map[string]string{
		"ListInstanceIDs token":     "12",
		"ListInstanceIDs key token": rowKey(instID(0), wfstate.MetadataKey),
		"not JSON":                  "bm90LWpzb24",
		"unknown version":           mustToken(t, `{"v":2,"t":"x"}`),
		"no store token":            mustToken(t, `{"v":1}`),
		"unknown field":             mustToken(t, `{"v":1,"t":"x","s":3}`),
		"trailing data":             mustToken(t, `{"v":1,"t":"x"}{}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ListInstances(t.Context(), listOpts(cs, nil, &protos.ListInstancesRequest{ContinuationToken: ptr.Of(tok)}))
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}

	t.Run("a token wraps the store token", func(t *testing.T) {
		t.Parallel()
		tok, err := encodeToken("abc")
		require.NoError(t, err)
		assert.Equal(t, mustToken(t, `{"v":1,"t":"abc"}`), tok)
		got, err := decodeToken(tok)
		require.NoError(t, err)
		assert.Equal(t, "abc", *got)
	})
}

// mustToken encodes a raw token body as ListInstances does.
func mustToken(t *testing.T, body string) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString([]byte(body))
}
