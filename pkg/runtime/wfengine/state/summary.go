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
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// summaryFacts holds what advance must know about the history and what the
// summary cannot hold. The rules follow addEventWithDedup and RuntimeStatus
// in durabletask-go backend/runtimestate/runtimestate.go. Keep them aligned
// when durabletask-go changes how it derives a status.
type summaryFacts struct {
	// started is true when the history holds an ExecutionStarted event.
	started bool

	// stalled is true when the last accepted history event is an
	// ExecutionStalled event.
	stalled bool

	// versioned is true when the history holds a WorkflowStarted event. The
	// first WorkflowStarted event sets the version.
	versioned bool

	// completedStatus is the status of the accepted ExecutionCompleted
	// event. It is valid only when the summary has completedAt.
	completedStatus protos.OrchestrationStatus
}

// pendingSummary is the summary that GetSaveRequest wrote into the metadata
// row. ResetChangeTracking makes it the committed summary when the state did
// not change after GetSaveRequest.
type pendingSummary struct {
	sum   *backend.BackendWorkflowSummary
	facts summaryFacts
	mark  summaryMark
}

// summaryMark identifies the changes that a pending summary covers.
type summaryMark struct {
	historyLen, historyAdded, historyRemoved int
	inboxLen, inboxAdded, inboxRemoved       int
}

// Summarize derives the summary of an instance from its full history and its
// persisted inbox. It is the reference derivation, and each load uses it. The
// save path uses the incremental advance, and a property test keeps the two
// equal. The result has the same values as GetWorkflowMetadata, except for an
// instance without an ExecutionStarted event in its history (PENDING): its
// name and parent come from the ExecutionStarted event in the inbox, and
// while its history is empty its lastUpdatedAt is its createdAt.
// GetWorkflowMetadata gives an empty name and a zero lastUpdatedAt for it.
//
// One difference from runtimestate stays: runtimestate ignores a duplicate
// result event (TaskCompleted, TaskFailed, TimerFired, or the completion or
// failure of a child workflow), and Summarize does not. The engine never
// writes such a duplicate to the history, because runtimestate.AddEvent
// rejects it before the event becomes a new event.
func Summarize(history, inbox []*backend.HistoryEvent) *backend.BackendWorkflowSummary {
	sum, _ := summarize(history, inbox)
	return sum
}

func summarize(history, inbox []*backend.HistoryEvent) (*backend.BackendWorkflowSummary, summaryFacts) {
	return advance(nil, summaryFacts{}, history, inbox)
}

// advance returns the summary of the history that base and facts describe,
// after the events in newEvents are added to it. It does not change base.
// Its cost is O(len(newEvents)). It reads the inbox only while the history
// has no ExecutionStarted event.
func advance(base *backend.BackendWorkflowSummary, facts summaryFacts, newEvents, inbox []*backend.HistoryEvent) (*backend.BackendWorkflowSummary, summaryFacts) {
	next := cloneSummary(base)

	var first, last, start, completed *backend.HistoryEvent
	for _, e := range newEvents {
		switch {
		case e.GetExecutionStarted() != nil:
			if facts.started {
				// runtimestate rejects a second ExecutionStarted event. A
				// rejected event does not change any other value.
				continue
			}
			facts.started = true
			start = e
		case e.GetExecutionCompleted() != nil:
			if next.GetCompletedAt() != nil || completed != nil {
				continue
			}
			completed = e
		case e.GetExecutionSuspended() != nil:
			next.Suspended = true
		case e.GetExecutionResumed() != nil:
			next.Suspended = false
		case e.GetWorkflowStarted() != nil:
			if !facts.versioned {
				facts.versioned = true
				if v := e.GetWorkflowStarted().GetVersion().GetName(); v != "" {
					next.Version = &v
				}
			}
		}

		// Every accepted event clears a stall, except a stall event.
		facts.stalled = e.GetExecutionStalled() != nil
		if first == nil && next.GetStartedAt() == nil {
			first = e
		}
		last = e
	}

	if first != nil {
		next.StartedAt = eventTime(first)
	}
	if completed != nil {
		next.CompletedAt = eventTime(completed)
		facts.completedStatus = completed.GetExecutionCompleted().GetWorkflowStatus()
	}

	switch {
	case start != nil:
		setStart(next, start)
	case !facts.started:
		// Until the first turn commits it, the start event is in the inbox.
		setStart(next, inboxStart(inbox))
	}

	switch {
	case last != nil:
		next.LastUpdatedAt = eventTime(last)
	case next.GetStartedAt() == nil:
		next.LastUpdatedAt = next.GetCreatedAt()
	}

	next.RuntimeStatus = summaryStatus(next, facts)
	return next, facts
}

// summaryStatus mirrors runtimestate.RuntimeStatus.
func summaryStatus(sum *backend.BackendWorkflowSummary, facts summaryFacts) protos.OrchestrationStatus {
	switch {
	case !facts.started:
		if facts.stalled {
			return protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED
		}
		return protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING
	case sum.GetCompletedAt() != nil:
		return facts.completedStatus
	case facts.stalled:
		return protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED
	case sum.GetSuspended():
		return protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED
	default:
		return protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING
	}
}

// MatchesFilter reports whether the instance that sum describes matches
// filter. A nil filter matches all instances. A time range includes its
// "from" time and excludes its "to" time. An instance without the time that
// a range bounds does not match that range.
//
// The duration of an instance is its completed time minus its created time.
// For an instance that did not complete, it is now minus its created time.
// The minimum duration is inclusive and the maximum duration is exclusive. An
// unset bound does not limit the duration. An instance without a created time
// does not match a duration bound.
func MatchesFilter(sum *backend.BackendWorkflowSummary, filter *protos.ListInstancesFilter, now time.Time) bool {
	if filter == nil {
		return true
	}
	if len(filter.GetRuntimeStatus()) > 0 && !slices.Contains(filter.GetRuntimeStatus(), sum.GetRuntimeStatus()) {
		return false
	}
	return inRange(sum.GetCreatedAt(), filter.GetCreatedTimeFrom(), filter.GetCreatedTimeTo()) &&
		inRange(sum.GetCompletedAt(), filter.GetCompletedTimeFrom(), filter.GetCompletedTimeTo()) &&
		inDuration(sum, filter.GetMinDuration(), filter.GetMaxDuration(), now)
}

func inRange(t, from, to *timestamppb.Timestamp) bool {
	if from == nil && to == nil {
		return true
	}
	if t == nil {
		return false
	}
	tt := t.AsTime()
	if from != nil && tt.Before(from.AsTime()) {
		return false
	}
	return to == nil || tt.Before(to.AsTime())
}

// inDuration reports whether the duration of the instance that sum describes
// is at least minDuration and less than maxDuration.
func inDuration(sum *backend.BackendWorkflowSummary, minDuration, maxDuration *durationpb.Duration, now time.Time) bool {
	if minDuration == nil && maxDuration == nil {
		return true
	}
	created := sum.GetCreatedAt()
	if created == nil {
		return false
	}
	end := now
	if c := sum.GetCompletedAt(); c != nil {
		end = c.AsTime()
	}
	d := end.Sub(created.AsTime())
	if minDuration != nil && d < minDuration.AsDuration() {
		return false
	}
	return maxDuration == nil || d < maxDuration.AsDuration()
}

// setStart sets the values that come from the ExecutionStarted event e. A nil
// e clears them.
func setStart(sum *backend.BackendWorkflowSummary, e *backend.HistoryEvent) {
	es := e.GetExecutionStarted()
	sum.Name = es.GetName()
	sum.ParentInstanceId = es.GetParentInstance().GetWorkflowInstance().GetInstanceId()
	sum.ParentAppId = nil
	if p := es.GetParentInstance(); p != nil && p.AppID != nil {
		v := p.GetAppID()
		sum.ParentAppId = &v
	}
	sum.CreatedAt = nil
	if e != nil {
		sum.CreatedAt = eventTime(e)
	}
}

// inboxStart returns the first ExecutionStarted event in inbox, or nil.
func inboxStart(inbox []*backend.HistoryEvent) *backend.HistoryEvent {
	for _, e := range inbox {
		if e.GetExecutionStarted() != nil {
			return e
		}
	}
	return nil
}

// eventTime copies the timestamp of e as runtimestate does: an unset
// timestamp becomes the Unix epoch.
func eventTime(e *backend.HistoryEvent) *timestamppb.Timestamp {
	return timestamppb.New(e.GetTimestamp().AsTime())
}

// cloneSummary returns a copy of sum, or an empty summary for a nil sum. The
// copy shares the timestamps, which no code changes after it sets them.
func cloneSummary(sum *backend.BackendWorkflowSummary) *backend.BackendWorkflowSummary {
	if sum == nil {
		return new(backend.BackendWorkflowSummary)
	}
	return &backend.BackendWorkflowSummary{
		Name:             sum.GetName(),
		RuntimeStatus:    sum.GetRuntimeStatus(),
		CreatedAt:        sum.GetCreatedAt(),
		StartedAt:        sum.GetStartedAt(),
		LastUpdatedAt:    sum.GetLastUpdatedAt(),
		CompletedAt:      sum.GetCompletedAt(),
		Version:          sum.Version,
		ParentInstanceId: sum.GetParentInstanceId(),
		ParentAppId:      sum.ParentAppId,
		Suspended:        sum.GetSuspended(),
	}
}

// Summary returns the summary of the instance as of the last load or the
// last successful save. It is nil for a new state that was not saved. The
// caller must not change it.
func (s *State) Summary() *backend.BackendWorkflowSummary {
	return s.summary
}

// nextSummary returns the summary for the save that GetSaveRequest builds. It
// reads only the history events that this save adds. After a reset, those
// are the whole history.
func (s *State) nextSummary(inbox []*backend.HistoryEvent) (*backend.BackendWorkflowSummary, summaryFacts) {
	if s.summary == nil || s.historyRemovedCount > 0 {
		return summarize(s.History, inbox)
	}
	return advance(s.summary, s.summaryFacts, s.History[len(s.History)-s.historyAddedCount:], inbox)
}

func (s *State) currentSummaryMark() summaryMark {
	return summaryMark{
		historyLen:     len(s.History),
		historyAdded:   s.historyAddedCount,
		historyRemoved: s.historyRemovedCount,
		inboxLen:       len(s.Inbox),
		inboxAdded:     s.inboxAddedCount,
		inboxRemoved:   s.inboxRemovedCount,
	}
}

// commitSummary is called by ResetChangeTracking after a successful save.
// When the state did not change after GetSaveRequest, the summary in the
// saved row becomes the committed summary. Otherwise the committed summary
// is dropped, and the next save derives it again from the whole history.
func (s *State) commitSummary() {
	p := s.pendingSummary
	s.pendingSummary = nil
	if p != nil && p.mark == s.currentSummaryMark() {
		s.summary, s.summaryFacts = p.sum, p.facts
		return
	}
	if s.historyAddedCount > 0 || s.historyRemovedCount > 0 || s.inboxAddedCount > 0 || s.inboxRemovedCount > 0 {
		s.summary = nil
		s.summaryFacts = summaryFacts{}
	}
}
