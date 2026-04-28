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
	rules         []compiledRule
	defaultAction wfaclapi.PolicyAction // "allow" or "deny" - deny if any policy says deny

	// wildcardAllowed[caller][opType] is set when the caller has at least one
	// allow rule with name="*" for opType. Required for IsCallerKnown because
	// non-subject methods (AddWorkflowEvent, PurgeWorkflowState, ...) operate on
	// existing instances and we cannot extract the workflow/activity name from
	// the request. Only callers with an unconditional wildcard allow for the
	// relevant op type can be trusted to invoke them.
	wildcardAllowed map[callerKey]map[OperationType]struct{}

	// hasDeny[caller][opType] is set when the caller has any deny rule for
	// opType. A caller with a deny rule has conditional trust and must not be
	// granted access to non-subject methods even if they also have a wildcard
	// allow, since those methods could target an instance the caller is
	// specifically denied from.
	hasDeny map[callerKey]map[OperationType]struct{}

	// targetNs is the namespace of the target app these policies apply to.
	// Used by enforcement paths to reason about same- vs cross-ns callers.
	targetNs string
}

// callerKey identifies a caller by (namespace, appID).
type callerKey struct {
	Namespace string
	AppID     string
}

type compiledRule struct {
	callerKeys map[callerKey]struct{}
	operations []compiledOp
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
//
// targetNs is the namespace of the target app, used to resolve a nil
// Caller.Namespace to same-ns.
func Compile(policies []wfaclapi.WorkflowAccessPolicy, targetNs string) *CompiledPolicies {
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
				callerKeys: make(map[callerKey]struct{}, len(rule.Callers)),
			}
			for _, caller := range rule.Callers {
				ns := targetNs
				if caller.Namespace != nil {
					ns = *caller.Namespace
				}
				cr.callerKeys[callerKey{Namespace: ns, AppID: caller.AppID}] = struct{}{}
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

	// Track per-(caller, opType) whether the caller has a wildcard allow rule
	// (name="*", action=allow) and whether they have any deny rule. IsCallerKnown
	// requires the former and forbids the latter.
	wildcardAllowed := make(map[callerKey]map[OperationType]struct{})
	hasDeny := make(map[callerKey]map[OperationType]struct{})
	for i := range rules {
		for _, op := range rules[i].operations {
			for k := range rules[i].callerKeys {
				if op.action == wfaclapi.PolicyActionAllow && op.pattern == "*" {
					if wildcardAllowed[k] == nil {
						wildcardAllowed[k] = make(map[OperationType]struct{})
					}
					wildcardAllowed[k][op.opType] = struct{}{}
				}
				if op.action == wfaclapi.PolicyActionDeny {
					if hasDeny[k] == nil {
						hasDeny[k] = make(map[OperationType]struct{})
					}
					hasDeny[k][op.opType] = struct{}{}
				}
			}
		}
	}

	return &CompiledPolicies{
		rules:           rules,
		defaultAction:   defaultAction,
		wildcardAllowed: wildcardAllowed,
		hasDeny:         hasDeny,
		targetNs:        targetNs,
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
func (cp *CompiledPolicies) IsCallerKnown(callerNamespace, callerAppID string, opType OperationType) bool {
	if cp == nil {
		return true
	}

	k := callerKey{Namespace: callerNamespace, AppID: callerAppID}
	if _, ok := cp.hasDeny[k][opType]; ok {
		return false
	}
	_, ok := cp.wildcardAllowed[k][opType]
	return ok
}

// TargetNamespace reports the namespace of the target app these policies
// apply to. Used by enforcement paths to reason about same- vs cross-ns
// callers when the feature gate is enabled.
func (cp *CompiledPolicies) TargetNamespace() string {
	if cp == nil {
		return ""
	}
	return cp.targetNs
}

// Evaluate checks whether the given caller is allowed to perform the specified
// operation. Returns true if allowed, false if denied.
func (cp *CompiledPolicies) Evaluate(callerNamespace, callerAppID string, opType OperationType, opName string) bool {
	if cp == nil {
		// No policies means allow all (backward compatible).
		return true
	}

	key := callerKey{Namespace: callerNamespace, AppID: callerAppID}

	var bestMatch *compiledOp
	for i := range cp.rules {
		rule := &cp.rules[i]

		// Check if this rule applies to the caller.
		if len(rule.callerKeys) > 0 {
			if _, ok := rule.callerKeys[key]; !ok {
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
func EnforceRequest(policies *CompiledPolicies, callerNamespace, callerAppID string, actorType string, method string, data []byte) (*EnforceRequestResult, error) {
	opType, isWorkflowOrActivityActor := ParseActorType(actorType)
	if !isWorkflowOrActivityActor {
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
		allowed := policies.IsCallerKnown(callerNamespace, callerAppID, opType)
		return &EnforceRequestResult{
			Allowed:   allowed,
			OpType:    opType,
			Operation: method,
		}, nil
	}

	allowed := policies.Evaluate(callerNamespace, callerAppID, opType, opName)
	return &EnforceRequestResult{
		Allowed:   allowed,
		OpType:    opType,
		Operation: "schedule",
	}, nil
}
