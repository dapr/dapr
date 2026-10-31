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

package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
	wffake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
	"github.com/dapr/kit/ptr"
)

type typedEngine struct{ *wffake.Fake }

func (typedEngine) WorkflowActorType() string { return "dapr.internal.default.myapp.workflow" }
func (typedEngine) ActivityActorType() string { return "dapr.internal.default.myapp.activity" }

func TestBuildNiceness(t *testing.T) {
	t.Parallel()

	engine := typedEngine{wffake.New()}
	wf, act := engine.WorkflowActorType(), engine.ActivityActorType()

	t.Run("no spec sends nothing", func(t *testing.T) {
		t.Parallel()
		c := New(Options{WFEngine: engine})
		assert.Nil(t, c.buildNiceness())
		assert.Nil(t, c.buildHostConcurrencyLimits())
	})

	t.Run("default and named entries", func(t *testing.T) {
		t.Parallel()
		c := New(Options{WFEngine: engine, WorkflowSpec: &config.WorkflowSpec{
			Niceness: &config.WorkflowNiceness{
				Default: ptr.Of(int32(5)),
				Workflows: []config.NamedNiceness{
					{Name: "Checkout", Niceness: -10},
					{Niceness: 3},
					{Name: "TooLow", Niceness: -50},
				},
				Activities: []config.NamedNiceness{
					{Name: "Newsletter", Niceness: 50},
				},
			},
		}})

		got := c.buildNiceness()
		require.NotNil(t, got)
		assert.Equal(t, int32(5), got.GetDefault())
		assert.Equal(t, map[string]int32{"Checkout": -10, "TooLow": -20}, got.GetWorkflows(), "unnamed entries are dropped, values clamped")
		assert.Equal(t, map[string]int32{"Newsletter": 19}, got.GetActivities())
	})

	t.Run("default is optional", func(t *testing.T) {
		t.Parallel()
		c := New(Options{WFEngine: engine, WorkflowSpec: &config.WorkflowSpec{
			Niceness: &config.WorkflowNiceness{
				Workflows: []config.NamedNiceness{{Name: "Checkout", Niceness: -10}},
			},
		}})

		got := c.buildNiceness()
		require.NotNil(t, got)
		assert.Equal(t, int32(0), got.GetDefault())
		assert.Nil(t, got.GetActivities())
	})

	t.Run("host limits are advertised per actor type", func(t *testing.T) {
		t.Parallel()
		c := New(Options{WFEngine: engine, WorkflowSpec: &config.WorkflowSpec{
			MaxConcurrentWorkflowInvocations: 10,
			MaxConcurrentActivityInvocations: 20,
		}})

		got := c.buildHostConcurrencyLimits()
		require.Len(t, got, 2)
		assert.Equal(t, wf, got[0].GetActor().GetType())
		assert.Equal(t, int32(10), got[0].GetMaxConcurrent())
		assert.Nil(t, got[0].Name)
		assert.Equal(t, act, got[1].GetActor().GetType())
		assert.Equal(t, int32(20), got[1].GetMaxConcurrent())
	})
}
