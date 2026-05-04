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
	"strings"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.acl.workflow")

// CompiledPolicies holds pre-processed workflow access policies for a single
// target app. It is built from one or more WorkflowAccessPolicy resources that
// apply to the app (via scopes).
type CompiledPolicies struct {
	rules         []compiledRule
	defaultAction wfaclapi.PolicyAction // "allow" or "deny" - deny if any policy says deny

	wildcardAllowed map[string]map[OperationType]struct{}
	hasDeny         map[string]map[OperationType]struct{}
}

type compiledRule struct {
	callerAppIDs map[string]struct{}
	operations   []compiledOp
}

type compiledOp struct {
	opType        OperationType
	operation     wfaclapi.WorkflowOperation
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

			for _, wf := range rule.Workflows {
				if len(wf.Operations) == 0 {
					log.Warnf("WorkflowAccessPolicy '%s' has a workflow rule with empty operations, skipping (defense-in-depth)", policy.Name)
					continue
				}
				if _, err := path.Match(wf.Name, ""); err != nil {
					log.Warnf("Invalid glob pattern '%s' in WorkflowAccessPolicy '%s', skipping workflow rule", wf.Name, policy.Name)
					continue
				}

				prefix := literalPrefixLen(wf.Name)
				exact := !containsWildcard(wf.Name)
				for _, operation := range wf.Operations {
					cr.operations = append(cr.operations, compiledOp{
						opType:        OperationTypeWorkflow,
						operation:     operation,
						pattern:       wf.Name,
						action:        wf.Action,
						literalPrefix: prefix,
						isExact:       exact,
					})
				}
			}

			for _, act := range rule.Activities {
				if _, err := path.Match(act.Name, ""); err != nil {
					log.Warnf("Invalid glob pattern '%s' in WorkflowAccessPolicy '%s', skipping activity rule", act.Name, policy.Name)
					continue
				}

				cr.operations = append(cr.operations, compiledOp{
					opType:        OperationTypeActivity,
					operation:     wfaclapi.WorkflowOperationSchedule,
					pattern:       act.Name,
					action:        act.Action,
					literalPrefix: literalPrefixLen(act.Name),
					isExact:       !containsWildcard(act.Name),
				})
			}

			if len(cr.operations) > 0 {
				rules = append(rules, cr)
			}
		}
	}

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

// Coarse fallback for reminders: caller needs a wildcard allow AND no
// denies for opType - any narrower rule means conditional trust, which
// could let the caller tamper with instances they're specifically denied.
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

// Evaluate checks whether the given caller is allowed to perform the specified
// operation. Returns true if allowed, false if denied.
func (cp *CompiledPolicies) Evaluate(callerAppID string, opType OperationType, operation wfaclapi.WorkflowOperation, opName string) bool {
	if cp == nil {
		// No policies means allow all (backward compatible).
		return true
	}

	var bestMatch *compiledOp
	for i := range cp.rules {
		rule := &cp.rules[i]

		if _, ok := rule.callerAppIDs[callerAppID]; !ok {
			continue
		}

		for j := range rule.operations {
			op := &rule.operations[j]

			if op.opType != opType {
				continue
			}
			if op.operation != operation {
				continue
			}

			matched, err := path.Match(op.pattern, opName)
			if err != nil || !matched {
				continue
			}

			if bestMatch == nil || isMoreSpecific(op, bestMatch) {
				bestMatch = op
			}
		}
	}

	if bestMatch == nil {
		// No rule matched - use the aggregate default action.
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
