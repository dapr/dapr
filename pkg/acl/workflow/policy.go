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

package workflow

import (
	"path"

	"google.golang.org/protobuf/proto"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.acl.workflow")

const DeniedMessageBase = "access denied by workflow access policy"

// DenialReason is for local metrics/logging only; never put on the wire.
type DenialReason string

const (
	DenialReasonNone          DenialReason = ""
	DenialReasonNotAllowed    DenialReason = "NotAllowed"
	DenialReasonRequiresUnmet DenialReason = "RequiresUnmet"
)

// CompiledPolicies holds pre-processed workflow access policies for a single
// target app, built from one or more WorkflowAccessPolicy resources scoped to
// the app. The policy is a pure allow-list: presence of a matching rule
// grants access; absence denies. A nil *CompiledPolicies means no policies
// are loaded, in which case all calls are allowed.
type CompiledPolicies struct {
	rules []compiledRule
}

type compiledRule struct {
	callerAppIDs map[string]struct{}
	operations   []compiledOp
}

type compiledOp struct {
	opType    OperationType
	operation wfaclapi.WorkflowOperation
	pattern   string
	requires  []wfaclapi.RequiredEvent
}

func Compile(policies []wfaclapi.WorkflowAccessPolicy) *CompiledPolicies {
	if len(policies) == 0 {
		return nil
	}

	var rules []compiledRule
	for _, policy := range policies {
		for _, rule := range policy.Spec.Rules {
			// Defense-in-depth: skip rules with empty callers. The CRD
			// validates MinItems=1 but standalone mode could bypass validation.
			if len(rule.Callers) == 0 {
				log.Warnf("WorkflowAccessPolicy '%s' has a rule with empty callers, skipping (defense-in-depth)", policy.Name)
				continue
			}

			cr := compiledRule{
				callerAppIDs: make(map[string]struct{}, len(rule.Callers)),
			}
			for _, caller := range rule.Callers {
				cr.callerAppIDs[caller.AppID] = struct{}{}
			}

			for _, wf := range rule.Workflows {
				if len(wf.Operations) == 0 {
					log.Warnf("WorkflowAccessPolicy '%s' has a workflow rule with empty operations, skipping (defense-in-depth)", policy.Name)
					continue
				}
				if _, err := path.Match(wf.Name, ""); err != nil {
					log.Warnf("Invalid glob pattern '%s' in WorkflowAccessPolicy '%s', skipping workflow rule", wf.Name, policy.Name)
					continue
				}

				for _, op := range wf.Operations {
					var requires []wfaclapi.RequiredEvent
					if len(wf.Requires) > 0 {
						// requires is only valid when the rule's sole
						// operation is schedule
						if op != wfaclapi.WorkflowOperationSchedule {
							log.Warnf("WorkflowAccessPolicy '%s': requires is only valid when the rule's only operation is schedule, skipping operation '%s' on workflow '%s'", policy.Name, op, wf.Name)
							continue
						}
						requires = wf.Requires
					}
					cr.operations = append(cr.operations, compiledOp{
						opType:    OperationTypeWorkflow,
						operation: op,
						pattern:   wf.Name,
						requires:  requires,
					})
				}
			}

			for _, act := range rule.Activities {
				if _, err := path.Match(act.Name, ""); err != nil {
					log.Warnf("Invalid glob pattern '%s' in WorkflowAccessPolicy '%s', skipping activity rule", act.Name, policy.Name)
					continue
				}

				cr.operations = append(cr.operations, compiledOp{
					opType:    OperationTypeActivity,
					operation: wfaclapi.WorkflowOperationSchedule,
					pattern:   act.Name,
					requires:  act.Requires,
				})
			}

			if len(cr.operations) > 0 {
				rules = append(rules, cr)
			}
		}
	}

	return &CompiledPolicies{rules: rules}
}

// Evaluate returns whether any rule grants the caller access to perform the
// operation on opName. Pass nil for history when the call carries no
// propagated history (operations other than schedule). On deny it also
// returns a DenialReason: RequiresUnmet when a rule matched on caller +
// name + operation but its Requires block wasn't satisfied; otherwise
// NotAllowed. A nil *CompiledPolicies means no policies are loaded and
// all calls are allowed.
func (cp *CompiledPolicies) Evaluate(callerAppID string, opType OperationType, operation wfaclapi.WorkflowOperation, opName string, history *protos.PropagatedHistory) (bool, DenialReason) {
	if cp == nil {
		return true, DenialReasonNone
	}

	// decode propagated history on the first matching rule that
	// actually carries a requires block. Subsequent rules reuse the cached
	// result. Skipped entirely if no matching rule has requires.
	var (
		chunks        []decodedChunk
		chunksDecoded bool
		chunksOK      bool
	)

	var requiresNotMet bool
	for i := range cp.rules {
		rule := &cp.rules[i]

		if _, ok := rule.callerAppIDs[callerAppID]; !ok {
			continue
		}

		for j := range rule.operations {
			op := &rule.operations[j]

			if op.opType != opType || op.operation != operation {
				continue
			}

			matched, err := path.Match(op.pattern, opName)
			if err != nil || !matched {
				continue
			}

			if len(op.requires) > 0 {
				if !chunksDecoded {
					if history != nil && len(history.GetChunks()) > 0 {
						chunks, chunksOK = decodeHistoryChunks(history)
					} else {
						chunksOK = true
					}
					chunksDecoded = true
				}
				if !chunksOK || len(chunks) == 0 || !requiresMatchChunks(op.requires, chunks) {
					requiresNotMet = true
					continue
				}
			}

			return true, DenialReasonNone
		}
	}

	if requiresNotMet {
		return false, DenialReasonRequiresUnmet
	}
	return false, DenialReasonNotAllowed
}

// ListsCaller reports whether any compiled rule names appID in its Callers
// list. Per-actor enforcement uses this on the self-call exemption path to
// detect a misconfigured policy that lists the local appID — that listing
// has no effect because same-app calls are always exempt.
func (cp *CompiledPolicies) ListsCaller(appID string) bool {
	if cp == nil {
		return false
	}
	for i := range cp.rules {
		if _, ok := cp.rules[i].callerAppIDs[appID]; ok {
			return true
		}
	}
	return false
}

// Lookups are scoped per-chunk: event IDs only unique within their producer.
type decodedChunk struct {
	chunk             *protos.PropagatedHistoryChunk
	events            []*protos.HistoryEvent
	taskScheduledByID map[int32]*protos.TaskScheduledEvent
	childCreatedByID  map[int32]*protos.ChildWorkflowInstanceCreatedEvent
}

// Fails closed: unmarshal failure means a tampered/corrupted chunk.
func decodeHistoryChunks(history *protos.PropagatedHistory) ([]decodedChunk, bool) {
	if history == nil {
		return nil, true
	}
	chunks := make([]decodedChunk, 0, len(history.GetChunks()))
	for _, chunk := range history.GetChunks() {
		if chunk == nil {
			continue
		}
		dc := decodedChunk{
			chunk:             chunk,
			taskScheduledByID: make(map[int32]*protos.TaskScheduledEvent),
			childCreatedByID:  make(map[int32]*protos.ChildWorkflowInstanceCreatedEvent),
		}
		for _, raw := range chunk.GetRawEvents() {
			e := &protos.HistoryEvent{}
			if err := proto.Unmarshal(raw, e); err != nil {
				log.Warnf("WorkflowAccessPolicy: malformed raw event in chunk (app=%q): %v", chunk.GetAppId(), err)
				return nil, false
			}
			dc.events = append(dc.events, e)
			if ts := e.GetTaskScheduled(); ts != nil {
				dc.taskScheduledByID[e.GetEventId()] = ts
			}
			if cc := e.GetChildWorkflowInstanceCreated(); cc != nil {
				dc.childCreatedByID[e.GetEventId()] = cc
			}
		}
		chunks = append(chunks, dc)
	}
	return chunks, true
}

func requiresMatchChunks(requires []wfaclapi.RequiredEvent, chunks []decodedChunk) bool {
	for i := range requires {
		if !findMatchingEvent(&requires[i], chunks) {
			return false
		}
	}
	return true
}

func findMatchingEvent(req *wfaclapi.RequiredEvent, chunks []decodedChunk) bool {
	for _, dc := range chunks {
		// TODO: gate on chunk namespace once durabletask-go's chunk carries it.
		if dc.chunk.GetAppId() != req.AppID {
			continue
		}
		for _, e := range dc.events {
			if eventStatusMatches(req, e, dc.taskScheduledByID, dc.childCreatedByID) {
				return true
			}
		}
	}
	return false
}

// eventStatusMatches reports whether e is the kind of history event described by req.
func eventStatusMatches(
	req *wfaclapi.RequiredEvent,
	e *protos.HistoryEvent,
	taskScheduledByID map[int32]*protos.TaskScheduledEvent,
	childCreatedByID map[int32]*protos.ChildWorkflowInstanceCreatedEvent,
) bool {
	switch req.EventType {
	case wfaclapi.RequiredEventTypeActivity:
		switch req.Status {
		case wfaclapi.RequiredStatusStarted:
			ts := e.GetTaskScheduled()
			return ts != nil && ts.GetName() == req.Name
		case wfaclapi.RequiredStatusCompleted:
			tc := e.GetTaskCompleted()
			if tc == nil {
				return false
			}
			ts, ok := taskScheduledByID[tc.GetTaskScheduledId()]
			return ok && ts.GetName() == req.Name
		}

	case wfaclapi.RequiredEventTypeWorkflow:
		switch req.Status {
		case wfaclapi.RequiredStatusStarted:
			// "workflow" started covers either the workflow's own
			// ExecutionStarted or a child-workflow creation.
			if es := e.GetExecutionStarted(); es != nil && es.GetName() == req.Name {
				return true
			}
			if cwc := e.GetChildWorkflowInstanceCreated(); cwc != nil && cwc.GetName() == req.Name {
				return true
			}
			return false
		case wfaclapi.RequiredStatusCompleted:
			// ExecutionCompleted carries no name on the event itself, so a
			// name filter only matches a child-workflow completion.
			if cwc := e.GetChildWorkflowInstanceCompleted(); cwc != nil {
				cc, ok := childCreatedByID[cwc.GetTaskScheduledId()]
				return ok && cc.GetName() == req.Name
			}
			return false
		}

	case wfaclapi.RequiredEventTypeEvent:
		if req.Status != wfaclapi.RequiredStatusRaised {
			return false
		}
		er := e.GetEventRaised()
		return er != nil && er.GetName() == req.Name
	}
	return false
}
