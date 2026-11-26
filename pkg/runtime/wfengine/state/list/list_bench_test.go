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
	"fmt"
	"testing"

	"github.com/dapr/dapr/pkg/runtime/compstore"
	wfstate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/ptr"
)

const (
	benchInstances = 10_000
	// benchExtraEvents makes about 40 history rows per instance.
	benchExtraEvents = 36
)

func benchStore(b *testing.B) (*fakeStore, *compstore.ComponentStore) {
	b.Helper()
	s := newFakeStore()
	for i := range benchInstances {
		h, in := historyFor(allKinds[i%len(allKinds)], "wf", i%60, benchExtraEvents)
		putInstance(b, s, fmt.Sprintf("inst-%05d", i), h, in, false)
	}
	return s, compStoreFor(b, s)
}

var benchFilter = &protos.ListInstancesFilter{RuntimeStatus: []protos.OrchestrationStatus{
	protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING,
	protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED,
}}

// BenchmarkListFiltered10k compares one filtered walk over 10k instances:
// ListInstances reads one metadata row per instance; the client loop today
// lists the IDs and loads each instance in full to read its status.
func BenchmarkListFiltered10k(b *testing.B) {
	s, cs := benchStore(b)
	want := benchInstances * 2 / len(allKinds)

	b.Run("ListInstances", func(b *testing.B) {
		s.rowsRead.Store(0)
		s.keysLikeCalls.Store(0)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			var n int
			var tok *string
			for {
				resp, err := ListInstances(b.Context(), ListInstancesOptions{
					ComponentStore: cs, Namespace: testNamespace, AppID: testAppID, Derive: loadDerive(s),
					Request: &protos.ListInstancesRequest{Filter: benchFilter, ContinuationToken: tok},
				})
				if err != nil {
					b.Fatal(err)
				}
				n += len(resp.GetInstances())
				if tok = resp.ContinuationToken; tok == nil {
					break
				}
			}
			if n != want {
				b.Fatalf("got %d instances, want %d", n, want)
			}
		}
		b.ReportMetric(float64(s.rowsRead.Load())/float64(b.N), "rows/op")
		b.ReportMetric(float64(s.keysLikeCalls.Load())/float64(b.N), "keyslike/op")
	})

	b.Run("ListInstanceIDs+load", func(b *testing.B) {
		s.rowsRead.Store(0)
		s.keysLikeCalls.Store(0)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			var n int
			var tok *string
			for {
				resp, err := ListInstanceIDs(b.Context(), ListOptions{
					ComponentStore: cs, Namespace: testNamespace, AppID: testAppID,
					PageSize: ptr.Of(uint32(1024)), ContinuationToken: tok,
				})
				if err != nil {
					b.Fatal(err)
				}
				for _, id := range resp.Keys {
					// What GetWorkflowMetadata does for each ID.
					ws, err := wfstate.LoadWorkflowState(b.Context(), actorState{s}, id, testStateOpts)
					if err != nil {
						b.Fatal(err)
					}
					rs := runtimestate.NewWorkflowRuntimeState(id, ws.CustomStatus, ws.History)
					st := runtimestate.RuntimeStatus(rs)
					if st == protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING ||
						st == protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED {
						n++
					}
				}
				if tok = resp.ContinuationToken; tok == nil {
					break
				}
			}
			if n != want {
				b.Fatalf("got %d instances, want %d", n, want)
			}
		}
		b.ReportMetric(float64(s.rowsRead.Load())/float64(b.N), "rows/op")
		b.ReportMetric(float64(s.keysLikeCalls.Load())/float64(b.N), "keyslike/op")
	})
}
