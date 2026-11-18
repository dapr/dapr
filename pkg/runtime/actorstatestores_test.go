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

package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"
)

func TestActorStateStoreBindings(t *testing.T) {
	actorsSpec := &config.ActorsSpec{StateStores: []config.ActorStateStoreBinding{
		{Name: "wallet-balance", ActorTypes: []string{"BalanceActor"}},
	}}

	t.Run("no bindings", func(t *testing.T) {
		assert.Empty(t, actorStateStoreBindings(config.ConfigurationSpec{}, "ns", "app"))
	})

	t.Run("spec.actors only", func(t *testing.T) {
		spec := config.ConfigurationSpec{
			ActorsSpec:   actorsSpec,
			WorkflowSpec: &config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 5},
		}
		assert.Equal(t, map[string]string{
			"BalanceActor": "wallet-balance",
		}, actorStateStoreBindings(spec, "ns", "app"))
	})

	t.Run("spec.workflow.stateStore binds the workflow actor types", func(t *testing.T) {
		spec := config.ConfigurationSpec{
			ActorsSpec:   actorsSpec,
			WorkflowSpec: &config.WorkflowSpec{StateStore: "wallet-workflows"},
		}
		assert.Equal(t, map[string]string{
			"BalanceActor":                     "wallet-balance",
			"dapr.internal.ns.app.workflow":    "wallet-workflows",
			"dapr.internal.ns.app.activity":    "wallet-workflows",
			"dapr.internal.ns.app.executor":    "wallet-workflows",
			"dapr.internal.ns.app.retentioner": "wallet-workflows",
		}, actorStateStoreBindings(spec, "ns", "app"))
	})
}
