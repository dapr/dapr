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
	"fmt"
	"path"
	"strings"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.acl.workflow")

// CompiledPolicies holds pre-processed workflow access policies for a single
// target app. It is built from one or more WorkflowAccessPolicy resources that
// apply to the app (via scopes).
type CompiledPolicies struct {
	rules         []compiledRule
	defaultAction wfaclapi.PolicyAction // "allow" or "deny" - deny if any policy says deny

	// wildcardAllowed[caller][opType] is set when the caller has at least one
	// allow rule with name="*" for opType. Required for IsCallerKnown because
	// non-subject methods (AddWorkflowEvent, PurgeWorkflowState, ...) operate on
	// existing instances and we cannot extract the workflow/activity name from
	// the request. Only callers with an unconditional wildcard allow for the
	// relevant op type can be trusted to invoke them.
	wildcardAllowed map[string]map[OperationType]struct{}

	// hasDeny[caller][opType] is set when the caller has any deny rule for
	// opType. A caller with a deny rule has conditional trust and must not be
	// granted access to non-subject methods even if they also have a wildcard
	// allow, since those methods could target an instance the caller is
	// specifically denied from.
	hasDeny map[string]map[OperationType]struct{}
}

type compiledRule struct {
	callerAppIDs map[string]struct{}
	operations   []compiledOp
}

type compiledOp struct {
	opType        OperationType
	pattern       string
	action        wfaclapi.PolicyAction
	literalPrefix int // length of literal prefix before first wildcard
	isExact       bool
	// requires is the list of history events that must all be satisfied by
	// the caller's propagated history for this rule to apply
	requires []wfaclapi.RequiredEvent
}

// Compile builds a CompiledPolicies from a set of WorkflowAccessPolicy
// resources that all apply to the same target app. Returns nil if no policies
// are provided (indicating no access control).
func Compile(policies []wfaclapi.WorkflowAccessPolicy) *CompiledPolicies {
	if len(policies) == 0 {
		return nil
	}

	// Compute the aggregate default action. If ANY policy specifies "deny"
	// (or leaves it empty, which defaults to "deny"), the result is deny.
	// Only if ALL policies explicitly say "allow" does the default become allow.
	defaultAction := wfaclapi.PolicyActionAllow
	for _, policy := range policies {
		da := policy.Spec.DefaultAction
		if da == "" || da == wfaclapi.PolicyActionDeny {
			defaultAction = wfaclapi.PolicyActionDeny
			break
		}
	}

	var rules []compiledRule
	for _, policy := range policies {
		for _, rule := range policy.Spec.Rules {
			// Defense-in-depth: skip rules with empty callers. The CRD
			// validates MinItems=1 but standalone mode could bypass validation.
			// An empty callers list would match ALL callers, violating
			// least-privilege.
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

			for _, op := range rule.Operations {
				// Validate glob pattern
				if _, err := path.Match(op.Name, ""); err != nil {
					log.Warnf("Invalid glob pattern '%s' in WorkflowAccessPolicy '%s', skipping operation", op.Name, policy.Name)
					continue
				}

				co := compiledOp{
					opType:        OperationType(op.Type),
					pattern:       op.Name,
					action:        op.Action,
					literalPrefix: literalPrefixLen(op.Name),
					isExact:       !containsWildcard(op.Name),
					requires:      op.Requires,
				}
				cr.operations = append(cr.operations, co)
			}

			if len(cr.operations) > 0 {
				rules = append(rules, cr)
			}
		}
	}

	// Track per-(caller, opType) whether the caller has a wildcard allow rule
	// (name="*", action=allow) and whether they have any deny rule. IsCallerKnown
	// requires the former and forbids the latter.
	wildcardAllowed := make(map[string]map[OperationType]struct{})
	hasDeny := make(map[string]map[OperationType]struct{})
	for i := range rules {
		for _, op := range rules[i].operations {
			for id := range rules[i].callerAppIDs {
				if op.action == wfaclapi.PolicyActionAllow && op.pattern == "*" {
					if wildcardAllowed[id] == nil {
						wildcardAllowed[id] = make(map[OperationType]struct{})
					}
					wildcardAllowed[id][op.opType] = struct{}{}
				}
				if op.action == wfaclapi.PolicyActionDeny {
					if hasDeny[id] == nil {
						hasDeny[id] = make(map[OperationType]struct{})
					}
					hasDeny[id][op.opType] = struct{}{}
				}
			}
		}
	}

	return &CompiledPolicies{
		rules:           rules,
		defaultAction:   defaultAction,
		wildcardAllowed: wildcardAllowed,
		hasDeny:         hasDeny,
	}
}

// IsCallerKnown reports whether the caller is unconditionally trusted to
// invoke non-subject methods (e.g., AddWorkflowEvent, PurgeWorkflowState) on
// actors of opType. Non-subject methods operate on existing workflow/activity
// instances whose name cannot be extracted from the request payload, so we
// cannot check the policy against the specific instance. The caller must
// therefore have an allow rule with name="*" for opType AND no deny rules for
// the same opType — anything narrower means the caller has conditional trust
// and could otherwise tamper with instances they are explicitly denied.
func (cp *CompiledPolicies) IsCallerKnown(callerAppID string, opType OperationType) bool {
	if cp == nil {
		return true
	}

	if _, ok := cp.hasDeny[callerAppID][opType]; ok {
		return false
	}
	_, ok := cp.wildcardAllowed[callerAppID][opType]
	return ok
}

// Evaluate checks whether the given caller is allowed to perform the
// specified operation. Pass nil for history when none is available. Rules
// whose Requires block can't be satisfied by the given history are skipped,
// letting other rules / the default action apply. On deny, returns a
// DenialReason: RequiresUnmet when an allow rule was skipped solely due to
// unmet Requires, otherwise NotAllowed.
func (cp *CompiledPolicies) Evaluate(callerAppID string, opType OperationType, opName string, history *protos.PropagatedHistory) (bool, DenialReason) {
	if cp == nil {
		// No policies means allow all (backward compatible).
		return true, DenialReasonNone
	}

	var bestMatch *compiledOp
	var requiresNotMet bool
	for i := range cp.rules {
		rule := &cp.rules[i]

		// Check if this rule applies to the caller.
		if len(rule.callerAppIDs) > 0 {
			if _, ok := rule.callerAppIDs[callerAppID]; !ok {
				continue
			}
		}

		for j := range rule.operations {
			op := &rule.operations[j]

			// Check operation type matches.
			if op.opType != opType {
				continue
			}

			// Check name matches glob pattern.
			matched, err := path.Match(op.pattern, opName)
			if err != nil || !matched {
				continue
			}

			if len(op.requires) > 0 && !requiresSatisfied(op.requires, history) {
				if op.action == wfaclapi.PolicyActionAllow {
					requiresNotMet = true
				}
				continue
			}

			// Select the most specific match.
			if bestMatch == nil || isMoreSpecific(op, bestMatch) {
				bestMatch = op
			}
		}
	}

	if bestMatch == nil {
		if cp.defaultAction == wfaclapi.PolicyActionAllow {
			return true, DenialReasonNone
		}
		if requiresNotMet {
			return false, DenialReasonRequiresUnmet
		}
		return false, DenialReasonNotAllowed
	}

	if bestMatch.action == wfaclapi.PolicyActionAllow {
		return true, DenialReasonNone
	}
	return false, DenialReasonNotAllowed
}

// isMoreSpecific returns true if a is more specific than b. Specificity rules
// (from proposal):
// 1. Longest literal prefix wins.
// 2. Exact match beats wildcard match.
// 3. Deny beats allow at same specificity.
func isMoreSpecific(a, b *compiledOp) bool {
	if a.literalPrefix != b.literalPrefix {
		return a.literalPrefix > b.literalPrefix
	}
	if a.isExact != b.isExact {
		return a.isExact
	}
	// Deny takes precedence over allow at same specificity (fail-closed).
	if a.action != b.action {
		return a.action == wfaclapi.PolicyActionDeny
	}
	return false
}

// literalPrefixLen returns the length of the string before the first wildcard
// character (*, ?, or [).
func literalPrefixLen(pattern string) int {
	for i, c := range pattern {
		if c == '*' || c == '?' || c == '[' {
			return i
		}
	}
	return len(pattern)
}

// containsWildcard returns true if the pattern contains any glob wildcard.
func containsWildcard(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// DenialReason explains why a request was denied; empty when allowed.
type DenialReason string

const (
	DenialReasonNone DenialReason = ""
	// NotAllowed: caller isn't in any matching allow rule, or the default action is deny.
	DenialReasonNotAllowed DenialReason = "NotAllowed"
	// RequiresUnmet: an allow rule matched on caller+name but its Requires block wasn't satisfied.
	DenialReasonRequiresUnmet DenialReason = "RequiresUnmet"
)

// EnforceRequestResult describes the outcome of a policy enforcement check.
type EnforceRequestResult struct {
	Allowed   bool
	OpType    OperationType
	Operation string // "schedule" for subject methods, or the method name
	Reason    DenialReason
}

// EnforceRequest evaluates compiled policies against an actor request and
// returns the policy decision. Used by both the gRPC handler (remote calls)
// and the local router (same-sidecar calls). Returns a nil result for
// non-workflow/activity actors or when policies is nil (allow-all).
func EnforceRequest(policies *CompiledPolicies, callerAppID string, actorType string, method string, data []byte) (*EnforceRequestResult, error) {
	opType, isWorkflowOrActivityActor := ParseActorType(actorType)
	if !isWorkflowOrActivityActor {
		return nil, nil
	}

	if policies == nil {
		return nil, nil
	}

	opName, history, subject, err := ExtractRequest(opType, method, data)
	if err != nil {
		return nil, fmt.Errorf("failed to extract operation name: %w", err)
	}

	if !subject {
		allowed := policies.IsCallerKnown(callerAppID, opType)
		res := &EnforceRequestResult{
			Allowed:   allowed,
			OpType:    opType,
			Operation: method,
		}
		if !allowed {
			res.Reason = DenialReasonNotAllowed
		}
		return res, nil
	}

	allowed, reason := policies.Evaluate(callerAppID, opType, opName, history)
	return &EnforceRequestResult{
		Allowed:   allowed,
		OpType:    opType,
		Operation: "schedule",
		Reason:    reason,
	}, nil
}

// requiresSatisfied reports whether every entry in `requires` can be located
// in the provided propagated history. Returns false if history is nil or
// empty when there is at least one requirement.
func requiresSatisfied(requires []wfaclapi.RequiredEvent, history *protos.PropagatedHistory) bool {
	if len(requires) == 0 {
		return true
	}
	if history == nil || len(history.GetEvents()) == 0 {
		return false
	}

	// Index TaskScheduled/ChildWorkflowInstanceCreated by event ID so
	// completion and failure events can resolve their name.
	events := history.GetEvents()
	taskScheduledByID := make(map[int32]*protos.TaskScheduledEvent, len(events))
	childCreatedByID := make(map[int32]*protos.ChildWorkflowInstanceCreatedEvent, len(events))
	for _, e := range events {
		if ts := e.GetTaskScheduled(); ts != nil {
			taskScheduledByID[e.GetEventId()] = ts
		}
		if cc := e.GetChildWorkflowInstanceCreated(); cc != nil {
			childCreatedByID[e.GetEventId()] = cc
		}
	}

	for i := range requires {
		if !findMatchingEvent(&requires[i], history, taskScheduledByID, childCreatedByID) {
			return false
		}
	}
	return true
}

// findMatchingEvent returns true when at least one event in history
// satisfies req.
func findMatchingEvent(
	req *wfaclapi.RequiredEvent,
	history *protos.PropagatedHistory,
	taskScheduledByID map[int32]*protos.TaskScheduledEvent,
	childCreatedByID map[int32]*protos.ChildWorkflowInstanceCreatedEvent,
) bool {
	events := history.GetEvents()
	chunks := history.GetChunks()
	for idx, e := range events {
		if !eventStatusMatches(req, e, taskScheduledByID, childCreatedByID) {
			continue
		}
		// TODO: also gate on chunk.GetNamespace() once durabletask-go's
		// PropagatedHistoryChunk carries the producing app's namespace; a
		// matching `Namespace` field would then live alongside `AppID` on
		// RequiredEvent.
		if req.AppID != "" {
			chunk := chunkForEventIndex(chunks, idx)
			if chunk == nil || chunk.GetAppId() != req.AppID {
				continue
			}
		}
		return true
	}
	return false
}

// eventStatusMatches returns true when `e` is the kind of history event
// described by `req`. Exactly one *Name field on `req` is set (validated by
// the CRD); it both narrows the category and enforces the name filter.
func eventStatusMatches(
	req *wfaclapi.RequiredEvent,
	e *protos.HistoryEvent,
	taskScheduledByID map[int32]*protos.TaskScheduledEvent,
	childCreatedByID map[int32]*protos.ChildWorkflowInstanceCreatedEvent,
) bool {
	switch req.Status {
	case wfaclapi.RequiredStatusStarted:
		switch {
		case req.ActivityName != "":
			ts := e.GetTaskScheduled()
			return ts != nil && ts.GetName() == req.ActivityName
		case req.WorkflowName != "":
			if es := e.GetExecutionStarted(); es != nil && es.GetName() == req.WorkflowName {
				return true
			}
			if cwc := e.GetChildWorkflowInstanceCreated(); cwc != nil && cwc.GetName() == req.WorkflowName {
				return true
			}
			return false
		}

	case wfaclapi.RequiredStatusCompleted:
		switch {
		case req.ActivityName != "":
			tc := e.GetTaskCompleted()
			if tc == nil {
				return false
			}
			ts, ok := taskScheduledByID[tc.GetTaskScheduledId()]
			return ok && ts.GetName() == req.ActivityName
		case req.WorkflowName != "":
			if cwc := e.GetChildWorkflowInstanceCompleted(); cwc != nil {
				cc, ok := childCreatedByID[cwc.GetTaskScheduledId()]
				return ok && cc.GetName() == req.WorkflowName
			}
			return false
		}

	case wfaclapi.RequiredStatusFailed:
		switch {
		case req.ActivityName != "":
			tf := e.GetTaskFailed()
			if tf == nil {
				return false
			}
			ts, ok := taskScheduledByID[tf.GetTaskScheduledId()]
			return ok && ts.GetName() == req.ActivityName
		case req.WorkflowName != "":
			cwf := e.GetChildWorkflowInstanceFailed()
			if cwf == nil {
				return false
			}
			cc, ok := childCreatedByID[cwf.GetTaskScheduledId()]
			return ok && cc.GetName() == req.WorkflowName
		}

	case wfaclapi.RequiredStatusRaised:
		if req.EventName == "" || req.ActivityName != "" || req.WorkflowName != "" {
			return false
		}
		er := e.GetEventRaised()
		return er != nil && er.GetName() == req.EventName
	}
	return false
}

// chunkForEventIndex returns the propagated chunk that owns the event at
// the given index in PropagatedHistory.events, or nil if not found.
func chunkForEventIndex(chunks []*protos.PropagatedHistoryChunk, eventIdx int) *protos.PropagatedHistoryChunk {
	for _, c := range chunks {
		start := int(c.GetStartEventIndex())
		end := start + int(c.GetEventCount())
		if eventIdx >= start && eventIdx < end {
			return c
		}
	}
	return nil
}
