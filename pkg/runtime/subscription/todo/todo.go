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

package todo

import (
	"maps"
	"sync"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
)

// Message contains all the essential information related to a particular entry.
// This need to be maintained as a separate struct, as we need to filter out messages and
// their related info doing retries of resiliency support.
type Message struct {
	CloudEvent map[string]any
	RawData    *pubsub.BulkSubscribeMessageItem
	Entry      *contribpubsub.BulkMessageEntry
}

// BulkSubscribedMessage contains all the essential information related to
// a bulk subscribe message.
type BulkSubscribedMessage struct {
	PubSubMessages []Message
	Topic          string
	Metadata       map[string]string
	Pubsub         string
	Path           string
	Length         int
}

// BulkSubIngressDiagnostics holds diagnostics information for bulk subscribe
// ingress.
//
// A single instance is shared by every resiliency attempt of one bulk
// delivery. When a policy timeout fires, the resiliency runner abandons the
// in-flight attempt and starts the next one straight away, so an abandoned
// attempt can still be recording its outcome while the attempt that replaced
// it records its own. Access is guarded by a lock so those overlapping writes
// are safe: without it, the concurrent map writes are liable to abort the
// process with a fatal error rather than merely lose a count.
type BulkSubIngressDiagnostics struct {
	lock           sync.Mutex
	statusWiseDiag map[string]int64
	elapsed        float64
	retryReported  bool
}

// AddStatusCount adds count to the number of entries recorded against status.
func (b *BulkSubIngressDiagnostics) AddStatusCount(status contribpubsub.AppResponseStatus, count int64) {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.statusWiseDiag[string(status)] += count
}

// SetElapsed records how long the delivery to the app took.
func (b *BulkSubIngressDiagnostics) SetElapsed(elapsed float64) {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.elapsed = elapsed
}

// SetRetryReported marks the entries of this delivery as already counted as
// retries, so that a subsequent dead letter of those entries moves them out of
// the retry count instead of counting them twice.
func (b *BulkSubIngressDiagnostics) SetRetryReported() {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.retryReported = true
}

// AddRetriesIfNotReported records count entries as retries, unless the
// entries of this delivery have already been counted as retries.
func (b *BulkSubIngressDiagnostics) AddRetriesIfNotReported(count int64) {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.retryReported {
		return
	}

	b.statusWiseDiag[string(contribpubsub.Retry)] += count
}

// AddDeadLettered records count entries as dropped to the dead letter topic,
// taking them back out of the retry count if they were reported as retries.
func (b *BulkSubIngressDiagnostics) AddDeadLettered(count int64) {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.statusWiseDiag[string(contribpubsub.Drop)] += count
	if b.retryReported {
		b.statusWiseDiag[string(contribpubsub.Retry)] -= count
	}
}

// snapshot returns a consistent copy of the counters for reporting.
func (b *BulkSubIngressDiagnostics) snapshot() (map[string]int64, float64) {
	b.lock.Lock()
	defer b.lock.Unlock()

	return maps.Clone(b.statusWiseDiag), b.elapsed
}

// BulkSubscribeCallData holds data for a bulk subscribe call.
type BulkSubscribeCallData struct {
	BulkResponses   *[]contribpubsub.BulkSubscribeResponseEntry
	BulkSubDiag     *BulkSubIngressDiagnostics
	EntryIdIndexMap *map[string]int //nolint:stylecheck
	PsName          string
	Topic           string
}

type BulkSubscribeResiliencyRes struct {
	Entries  []contribpubsub.BulkSubscribeResponseEntry
	Envelope map[string]any
}
