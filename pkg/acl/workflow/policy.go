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
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.acl.workflow")

// CompiledPolicies holds pre-processed workflow access policies for a single
// target app. It is built from one or more WorkflowAccessPolicy resources that
// apply to the app (via scopes).
type CompiledPolicies struct {
	rules          []compiledRule
	defaultAction  wfaclapi.PolicyAction // "allow" or "deny" - deny if any policy says deny
	allowedCallers map[string]struct{}   // callers that have at least one allow rule
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
				}
				cr.operations = append(cr.operations, co)
			}

			if len(cr.operations) > 0 {
				rules = append(rules, cr)
			}
		}
	}

	// Build the set of callers that have at least one allow action.
	// IsCallerKnown uses this to avoid granting access to callers that
	// only appear in deny rules.
	allowed := make(map[string]struct{})
	for i := range rules {
		for _, op := range rules[i].operations {
			if op.action == wfaclapi.PolicyActionAllow {
				for id := range rules[i].callerAppIDs {
					allowed[id] = struct{}{}
				}
				break
			}
		}
	}

	return &CompiledPolicies{rules: rules, defaultAction: defaultAction, allowedCallers: allowed}
}

// IsCallerKnown checks whether the given caller appears in at least one rule
// that contains an allow action. Used for methods where we can't extract a
// specific operation name (e.g., AddWorkflowEvent, PurgeWorkflowState).
// Callers that only appear in deny rules are not considered "known" to prevent
// a deny-only entry from granting broader access than being absent entirely.
func (cp *CompiledPolicies) IsCallerKnown(callerAppID string) bool {
	if cp == nil {
		return true
	}

	_, ok := cp.allowedCallers[callerAppID]
	return ok
}

// Evaluate checks whether the given caller is allowed to perform the specified
// operation. Returns true if allowed, false if denied.
func (cp *CompiledPolicies) Evaluate(callerAppID string, opType OperationType, opName string) bool {
	if cp == nil {
		// No policies means allow all (backward compatible).
		return true
	}

	var bestMatch *compiledOp
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

			// Select the most specific match.
			if bestMatch == nil || isMoreSpecific(op, bestMatch) {
				bestMatch = op
			}
		}
	}

	if bestMatch == nil {
		// No rule matched — use the aggregate default action.
		return cp.defaultAction == wfaclapi.PolicyActionAllow
	}

	return bestMatch.action == wfaclapi.PolicyActionAllow
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

// EnforceRequestResult describes the outcome of a policy enforcement check.
type EnforceRequestResult struct {
	Allowed   bool
	OpType    OperationType
	Operation string // "schedule" for subject methods, or the method name
}

// EnforceRequest evaluates compiled policies against an actor request. It
// parses the actor type, extracts the operation name, and returns the policy
// decision. This is the single enforcement path used by both the gRPC handler
// (remote calls) and the local router (same-sidecar calls).
// Returns nil result if the request is not for a workflow/activity actor or
// policies are nil (allow-all).
func EnforceRequest(policies *CompiledPolicies, callerAppID string, actorType string, method string, data []byte) (*EnforceRequestResult, error) {
	opType, isWorkflowActor := ParseActorType(actorType)
	if !isWorkflowActor {
		return nil, nil
	}

	if policies == nil {
		return nil, nil
	}

	opName, subject, err := ExtractOperationName(opType, method, data)
	if err != nil {
		return nil, fmt.Errorf("failed to extract operation name: %w", err)
	}

	if !subject {
		allowed := policies.IsCallerKnown(callerAppID)
		return &EnforceRequestResult{
			Allowed:   allowed,
			OpType:    opType,
			Operation: method,
		}, nil
	}

	allowed := policies.Evaluate(callerAppID, opType, opName)
	return &EnforceRequestResult{
		Allowed:   allowed,
		OpType:    opType,
		Operation: "schedule",
	}, nil
}
