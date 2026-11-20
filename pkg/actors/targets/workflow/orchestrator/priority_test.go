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

package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/durabletask-go/api/protos"
)

func TestChildStartPriority(t *testing.T) {
	t.Parallel()

	started := &protos.ExecutionStartedEvent{
		Name:     "parent",
		Priority: &protos.Priority{PriorityLevel: new("low"), FairnessWeight: new(uint32(4))},
	}

	o := &orchestrator{factory: &factory{}}
	got := o.childStartPriority(started)
	assert.Equal(t, "low", got.GetPriorityLevel(), "no resolver: the parent's start value")
	assert.Equal(t, uint32(4), got.GetFairnessWeight())
	assert.NotSame(t, started.GetPriority(), got, "a copy, not the parent's")
	assert.Nil(t, o.childStartPriority(&protos.ExecutionStartedEvent{Name: "other"}), "no resolver and no start value")

	spec := &config.WorkflowSpec{Priority: &config.WorkflowPriority{
		Default:   &config.PriorityValues{PriorityLevel: new(config.PriorityLevelHigh), FairnessWeight: new(uint32(2))},
		Workflows: []config.NamedPriorityValues{{Name: new("parent"), PriorityLevel: new(config.PriorityLevelCritical)}},
	}}
	o = &orchestrator{factory: &factory{workflowPriority: spec.WorkflowPriority}}

	got = o.childStartPriority(started)
	require.NotNil(t, got)
	assert.Equal(t, config.PriorityLevelCritical, got.GetPriorityLevel(), "the parent's entry")
	assert.Equal(t, uint32(4), got.GetFairnessWeight(), "the parent's start weight")
	assert.NotSame(t, started.GetPriority(), got, "a new message, not the parent's")

	got = o.childStartPriority(&protos.ExecutionStartedEvent{Name: "other"})
	assert.Equal(t, config.PriorityLevelHigh, got.GetPriorityLevel(), "the default")
	assert.Equal(t, uint32(2), got.GetFairnessWeight())

	o = &orchestrator{factory: &factory{workflowPriority: (*config.WorkflowSpec)(nil).WorkflowPriority}}
	assert.Nil(t, o.childStartPriority(&protos.ExecutionStartedEvent{Name: "other"}), "nothing resolved: nothing sent")
}
