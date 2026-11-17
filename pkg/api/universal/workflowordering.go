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

package universal

import (
	"errors"
	"fmt"

	"github.com/dapr/durabletask-go/api"
)

const (
	optionOrderingKey     = "dapr.workflow.ordering_key"
	optionOrderingFailure = "dapr.workflow.ordering_on_predecessor_failure"
)

// workflowOrdering reads the ordering of a StartWorkflow request from its
// options, nil when it has none.
func workflowOrdering(options map[string]string) (*api.Ordering, error) {
	key, keyed := options[optionOrderingKey]
	policy, hasPolicy := options[optionOrderingFailure]
	if !keyed {
		if hasPolicy {
			return nil, errors.New(optionOrderingFailure + " needs " + optionOrderingKey)
		}
		return nil, nil
	}
	o := &api.Ordering{Key: key}
	switch policy {
	case "", "continue":
	case "block":
		o.OnPredecessorFailure = api.OrderingFailurePolicyBlock
	case "cancel":
		o.OnPredecessorFailure = api.OrderingFailurePolicyCancel
	default:
		return nil, fmt.Errorf("invalid %s %q: it must be continue, block or cancel", optionOrderingFailure, policy)
	}
	return o, nil
}
