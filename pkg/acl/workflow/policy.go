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
	// AppIDs named by any requires entry. Chunks from other apps are
	// skipped without decoding.
	requiresAppIDs map[string]struct{}
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
	requiresAppIDs := make(map[string]struct{})
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
						if op != wfaclapi.WorkflowOperationSchedule {
							log.Warnf("WorkflowAccessPolicy '%s': requires is only valid when the rule's only operation is schedule, skipping operation '%s' on workflow '%s'", policy.Name, op, wf.Name)
							continue
						}
						requires = wf.Requires
					}
					for k := range requires {
						requiresAppIDs[requires[k].AppID] = struct{}{}
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

				for k := range act.Requires {
					requiresAppIDs[act.Requires[k].AppID] = struct{}{}
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

	return &CompiledPolicies{rules: rules, requiresAppIDs: requiresAppIDs}
}

// Evaluate returns whether any rule grants the caller access to perform the
// operation on opName. history is the caller's propagated history (nil for
// operations other than schedule). A `requires` block can never grant access
// while signingEnabled is false, since the history is then unverifiable. On
// deny it returns RequiresUnmet when a rule matched on caller+name+operation
// but its Requires wasn't satisfied, else NotAllowed. A nil *CompiledPolicies
// allows all calls.
func (cp *CompiledPolicies) Evaluate(callerAppID string, opType OperationType, operation wfaclapi.WorkflowOperation, opName string, history *protos.PropagatedHistory, signingEnabled bool) (bool, DenialReason) {
	if cp == nil {
		return true, DenialReasonNone
	}

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
				if !signingEnabled {
					requiresNotMet = true
					continue
				}
				if !chunksDecoded {
					if history != nil && len(history.GetChunks()) > 0 {
						chunks, chunksOK = decodeHistoryChunks(history, cp.requiresAppIDs)
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

// Lookups are scoped per-chunk: event IDs are only unique within their producer.
type decodedChunk struct {
	chunk  *protos.PropagatedHistoryChunk
	events []*protos.HistoryEvent

	// Built on the first Completed lookup; nil until then. Started/Raised
	// requirements never consult these, so a requires list with no Completed
	// entry never allocates them.
	taskScheduledByID map[int32]*protos.TaskScheduledEvent
	childCreatedByID  map[int32]*protos.ChildWorkflowInstanceCreatedEvent
}

// indexByID populates the per-chunk ID maps on first use. Cheap no-op once built.
func (dc *decodedChunk) indexByID() {
	if dc.taskScheduledByID != nil {
		return
	}
	dc.taskScheduledByID = make(map[int32]*protos.TaskScheduledEvent)
	dc.childCreatedByID = make(map[int32]*protos.ChildWorkflowInstanceCreatedEvent)
	for _, e := range dc.events {
		if ts := e.GetTaskScheduled(); ts != nil {
			dc.taskScheduledByID[e.GetEventId()] = ts
		}
		if cc := e.GetChildWorkflowInstanceCreated(); cc != nil {
			dc.childCreatedByID[e.GetEventId()] = cc
		}
	}
}

// Fails closed: unmarshal failure means a tampered/corrupted chunk.
func decodeHistoryChunks(history *protos.PropagatedHistory, appIDs map[string]struct{}) ([]decodedChunk, bool) {
	if history == nil {
		return nil, true
	}
	chunks := make([]decodedChunk, 0, len(history.GetChunks()))
	for _, chunk := range history.GetChunks() {
		if chunk == nil {
			continue
		}
		// Skip decoding a chunk if the app ID is not in the requires.
		// TODO: gate on chunk namespace once durabletask-go's chunk carries it.
		if _, ok := appIDs[chunk.GetAppId()]; !ok {
			continue
		}
		dc := decodedChunk{chunk: chunk}
		for _, raw := range chunk.GetRawEvents() {
			e := &protos.HistoryEvent{}
			if err := proto.Unmarshal(raw, e); err != nil {
				log.Warnf("WorkflowAccessPolicy: malformed raw event in chunk (app=%q): %v", chunk.GetAppId(), err)
				return nil, false
			}
			dc.events = append(dc.events, e)
		}
		chunks = append(chunks, dc)
	}
	return chunks, true
}

// requiresMatchChunks reports whether the history satisfies the requires list
// in order: each entry must match an event at or after the one that satisfied
// the previous entry, over the flattened event stream (chunk order, then event
// order). Iterate by index so the indexByID() build persists on the cached chunk.
func requiresMatchChunks(requires []wfaclapi.RequiredEvent, chunks []decodedChunk) bool {
	ri := 0
	for ci := range chunks {
		dc := &chunks[ci]
		for _, e := range dc.events {
			if ri >= len(requires) {
				return true
			}
			req := &requires[ri]
			if dc.chunk.GetAppId() != req.AppID {
				continue
			}
			if eventStatusMatches(req, e, dc) {
				ri++
			}
		}
	}
	return ri >= len(requires)
}

// eventStatusMatches reports whether e is the kind of history event described by req.
func eventStatusMatches(req *wfaclapi.RequiredEvent, e *protos.HistoryEvent, dc *decodedChunk) bool {
	switch req.EventType {
	case wfaclapi.RequiredEventTypeActivityStarted:
		ts := e.GetTaskScheduled()
		return ts != nil && ts.GetName() == req.Name

	case wfaclapi.RequiredEventTypeActivityCompleted:
		tc := e.GetTaskCompleted()
		if tc == nil {
			return false
		}
		dc.indexByID()
		ts, ok := dc.taskScheduledByID[tc.GetTaskScheduledId()]
		return ok && ts.GetName() == req.Name

	case wfaclapi.RequiredEventTypeWorkflowStarted:
		// A child-workflow creation, not the caller's own ExecutionStarted.
		cwc := e.GetChildWorkflowInstanceCreated()
		return cwc != nil && cwc.GetName() == req.Name

	case wfaclapi.RequiredEventTypeWorkflowCompleted:
		cwc := e.GetChildWorkflowInstanceCompleted()
		if cwc == nil {
			return false
		}
		dc.indexByID()
		cc, ok := dc.childCreatedByID[cwc.GetTaskScheduledId()]
		return ok && cc.GetName() == req.Name

	case wfaclapi.RequiredEventTypeEventRaised:
		er := e.GetEventRaised()
		return er != nil && er.GetName() == req.Name
	}
	return false
}
