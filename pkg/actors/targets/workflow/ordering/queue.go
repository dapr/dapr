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

package ordering

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/durabletask-go/api/protos"
)

const (
	// EnqueueMethod adds an Entry to its key's queue and answers whether it
	// was already there. It is idempotent, and re-creates the reminder of
	// the instance holding the key.
	EnqueueMethod = "Enqueue"
	// DoneMethod reports that an instance no longer holds or waits for its
	// key, with its Outcome. It is idempotent.
	DoneMethod = "Done"

	// The reminders that release or cancel an instance are named with these
	// prefixes and its create execution ID. Their data is a ReminderData, as
	// JSON in a StringValue.
	releaseReminderPrefix = "ordering-release-"
	cancelReminderPrefix  = "ordering-cancel-"

	maxKeyLength = 64
)

// Entry is an instance in its key's queue. An instance ID is reused across
// runs of an instance, so the create execution ID is part of its identity.
type Entry struct {
	InstanceID        string                       `json:"id"`
	CreateExecutionID string                       `json:"exec"`
	WorkflowName      string                       `json:"name,omitempty"`
	DueTime           *time.Time                   `json:"due,omitempty"`
	Policy            protos.OrderingFailurePolicy `json:"policy,omitempty"`
}

// Outcome is how an instance came to no longer hold or wait for its key.
type Outcome string

const (
	// OutcomeSucceeded is an instance that ran and completed.
	OutcomeSucceeded Outcome = "succeeded"
	// OutcomeFailed is an instance that ran and failed or was terminated.
	OutcomeFailed Outcome = "failed"
	// OutcomeSkipped is an instance that never ran, or a late duplicate. It
	// is nobody's predecessor.
	OutcomeSkipped Outcome = "skipped"
	// OutcomePurged is an instance that was purged, which also skips a
	// failure of its instance ID that holds up the key.
	OutcomePurged Outcome = "purged"
)

// DoneRequest is the payload of Done.
type DoneRequest struct {
	Entry
	Outcome Outcome `json:"outcome"`
}

// ReminderData is the data of a release or cancel reminder.
type ReminderData struct {
	Key string `json:"key"`
	// After is the instance ID of the failure the instance was released or
	// cancelled past, if any.
	After string `json:"after,omitempty"`
}

// ValidateKey rejects a key that is empty, longer than an instance ID may be,
// or holds characters other than letters, digits, '-' and '_'.
func ValidateKey(key string) error {
	if key == "" || utf8.RuneCountInString(key) > maxKeyLength || !common.ValidAppID(key) {
		return status.Errorf(codes.InvalidArgument, "invalid workflow ordering key %q: it must be 1 to %d letters, digits, '-' or '_'", key, maxKeyLength)
	}
	return nil
}

// ValidatePolicy rejects a failure policy that is not defined.
func ValidatePolicy(p protos.OrderingFailurePolicy) error {
	if _, ok := protos.OrderingFailurePolicy_name[int32(p)]; !ok {
		return status.Errorf(codes.InvalidArgument, "invalid workflow ordering failure policy %d", p)
	}
	return nil
}

func ReleaseReminderName(createExecutionID string) string {
	return releaseReminderPrefix + createExecutionID
}

func CancelReminderName(createExecutionID string) string {
	return cancelReminderPrefix + createExecutionID
}

// ParseReminderName reports whether name is a release or cancel reminder,
// which of the two, and the create execution ID it is for.
func ParseReminderName(name string) (exec string, cancel, ok bool) {
	if exec, ok = strings.CutPrefix(name, releaseReminderPrefix); ok {
		return exec, false, true
	}
	if exec, ok = strings.CutPrefix(name, cancelReminderPrefix); ok {
		return exec, true, true
	}
	return "", false, false
}

// head is the instance holding the key, with what it was told to do.
type head struct {
	Entry
	Cancel bool `json:"cancel,omitempty"`
	// After is the unresolved failure the instance was released or cancelled
	// past, which becomes the failure again if the instance never runs.
	After *Entry `json:"after,omitempty"`
}

// queue is a key's row. Last is the unresolved failure while no instance
// holds the key: Last != nil implies Head == nil.
type queue struct {
	Head  *head   `json:"head,omitempty"`
	Queue []Entry `json:"queue,omitempty"`
	Last  *Entry  `json:"last,omitempty"`
}

func (e Entry) is(o Entry) bool {
	return e.InstanceID == o.InstanceID && e.CreateExecutionID == o.CreateExecutionID
}

func (q *queue) empty() bool {
	return q.Head == nil && len(q.Queue) == 0 && q.Last == nil
}

// enqueue adds e unless it is already known, and returns the head whose
// reminder is to be created: a new head, or the existing one when e holds the
// key, so a repeat never turns a cancel into a release.
func (q *queue) enqueue(e Entry) (existing bool, assert *head) {
	switch {
	case q.Head != nil && q.Head.is(e):
		return true, q.Head
	case q.Last != nil && q.Last.is(e), slices.ContainsFunc(q.Queue, e.is):
		return true, nil
	}
	q.Queue = append(q.Queue, e)
	return false, q.advance()
}

// done records that e no longer holds or waits for the key. It reports
// whether that changed the queue, and the head whose reminder is to be
// created: a new head, or the current one when nothing changed, which
// repairs a reminder create that failed after a save.
func (q *queue) done(e Entry, o Outcome) (changed bool, assert *head) {
	if h := q.Head; h != nil && h.is(e) {
		q.Head = nil
		switch {
		case o == OutcomeSucceeded:
		case o == OutcomeFailed:
			q.Last = &h.Entry
		case h.After != nil && (o != OutcomePurged || h.After.InstanceID != e.InstanceID):
			q.Last = h.After
		}
		return true, q.advance()
	}

	if i := slices.IndexFunc(q.Queue, e.is); i >= 0 {
		q.Queue = slices.Delete(q.Queue, i, i+1)
		changed = true
	}
	if o == OutcomePurged {
		switch {
		case q.Last != nil && q.Last.InstanceID == e.InstanceID:
			q.Last, changed = nil, true
		case q.Head != nil && q.Head.After != nil && q.Head.After.InstanceID == e.InstanceID:
			q.Head.After, changed = nil, true
		}
	}
	if !changed {
		return false, q.Head
	}
	return true, q.advance()
}

// advance gives a free key to the next instance, unless its policy holds it
// back, and returns the new head. A newer run of the failed instance goes
// first, wherever it is queued: that is a retry in place.
func (q *queue) advance() *head {
	if q.Head != nil || len(q.Queue) == 0 {
		return nil
	}
	if q.Last != nil {
		if i := slices.IndexFunc(q.Queue, func(e Entry) bool { return e.InstanceID == q.Last.InstanceID }); i >= 0 {
			e := q.Queue[i]
			q.Queue = slices.Delete(q.Queue, i, i+1)
			return q.take(e, false)
		}
	}
	next := q.Queue[0]
	switch {
	case q.Last == nil, next.Policy == protos.OrderingFailurePolicy_ORDERING_FAILURE_POLICY_CONTINUE:
	case next.Policy == protos.OrderingFailurePolicy_ORDERING_FAILURE_POLICY_CANCEL:
		q.Queue = q.Queue[1:]
		return q.take(next, true)
	default:
		return nil
	}
	q.Queue = q.Queue[1:]
	return q.take(next, false)
}

func (q *queue) take(e Entry, cancel bool) *head {
	q.Head = &head{Entry: e, Cancel: cancel, After: q.Last}
	q.Last = nil
	return q.Head
}
