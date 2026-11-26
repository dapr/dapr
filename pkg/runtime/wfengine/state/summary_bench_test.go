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

package state

import (
	"strconv"
	"testing"

	"github.com/dapr/durabletask-go/backend"
)

var benchSizes = []int{10, 1_000, 10_000, 100_000}

func benchHistory(n int) []*backend.HistoryEvent {
	h := make([]*backend.HistoryEvent, 0, n)
	h = append(h, sevStarted(0, "v1"), sevExecStarted(0, "wf", nil))
	for i := int32(0); len(h) < n; i++ {
		h = append(h, sevTask(int(i), i))
		if len(h) < n {
			h = append(h, sevTaskDone(int(i), i))
		}
	}
	return h
}

// BenchmarkGetSaveRequestSummary measures one save that adds two events to
// a history of n events. The cost must not grow with n: the summary is
// advanced from the new events only.
func BenchmarkGetSaveRequestSummary(b *testing.B) {
	for _, n := range benchSizes {
		b.Run("history="+strconv.Itoa(n), func(b *testing.B) {
			s := NewState(testOpts())
			for _, e := range benchHistory(n) {
				s.AddToHistory(e)
			}
			if _, err := s.GetSaveRequest("wf-1"); err != nil {
				b.Fatal(err)
			}
			s.ResetChangeTracking()
			base, baseFacts := s.summary, s.summaryFacts
			task, done := sevTask(1, 1<<20), sevTaskDone(2, 1<<20)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				// Keep the history at n events, so every iteration saves the
				// same change.
				s.History = s.History[:n]
				s.summary, s.summaryFacts = base, baseFacts
				s.AddToHistory(task)
				s.AddToHistory(done)
				if _, err := s.GetSaveRequest("wf-1"); err != nil {
					b.Fatal(err)
				}
				s.ResetChangeTracking()
			}
		})
	}
}

// BenchmarkSummarize is the reference: the cost of a full derivation. Each
// load pays it once. A save would pay it if it read the whole history.
func BenchmarkSummarize(b *testing.B) {
	for _, n := range benchSizes {
		b.Run("history="+strconv.Itoa(n), func(b *testing.B) {
			h := benchHistory(n)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				Summarize(h, nil)
			}
		})
	}
}
