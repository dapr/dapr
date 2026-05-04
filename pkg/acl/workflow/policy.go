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

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.acl.workflow")

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

				for _, operation := range wf.Operations {
					cr.operations = append(cr.operations, compiledOp{
						opType:    OperationTypeWorkflow,
						operation: operation,
						pattern:   wf.Name,
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
				})
			}

			if len(cr.operations) > 0 {
				rules = append(rules, cr)
			}
		}
	}

	return &CompiledPolicies{rules: rules}
}

// Evaluate returns true if any rule grants the caller access to perform the
// operation on opName. A nil *CompiledPolicies means no policies are loaded
// and all calls are allowed.
func (cp *CompiledPolicies) Evaluate(callerAppID string, opType OperationType, operation wfaclapi.WorkflowOperation, opName string) bool {
	if cp == nil {
		return true
	}

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
			if err == nil && matched {
				return true
			}
		}
	}

	return false
}
