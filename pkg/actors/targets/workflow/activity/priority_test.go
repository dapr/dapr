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

package activity

import (
	"testing"

	"github.com/stretchr/testify/assert"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

func TestPriorityFromMetadata(t *testing.T) {
	t.Parallel()

	md := func(level, weight string) map[string]*internalsv1pb.ListStringValue {
		m := make(map[string]*internalsv1pb.ListStringValue)
		if level != "" {
			m[todo.MetadataActivityPriorityLevel] = &internalsv1pb.ListStringValue{Values: []string{level}}
		}
		if weight != "" {
			m[todo.MetadataActivityFairnessWeight] = &internalsv1pb.ListStringValue{Values: []string{weight}}
		}
		return m
	}

	assert.Equal(t, priority{}, priorityFromMetadata(nil), "no metadata leaves both unset")
	assert.Equal(t, priority{}, priorityFromMetadata(md("", "")))
	assert.Equal(t, priority{level: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL, weight: 3}, priorityFromMetadata(md("critical", "3")))
	assert.Equal(t, priority{weight: 7}, priorityFromMetadata(md("", "7")))
	assert.Equal(t, priority{weight: 1000}, priorityFromMetadata(md("", "1000")))
	assert.Equal(t, priority{}, priorityFromMetadata(md("", "0")), "a zero weight stays unset")
	assert.Equal(t, priority{}, priorityFromMetadata(md("", "-1")))
	assert.Equal(t, priority{}, priorityFromMetadata(md("", "0.5")), "a decimal is not a weight")
	assert.Equal(t, priority{}, priorityFromMetadata(md("", "abc")))
	for name, level := range map[string]schedulerv1pb.PriorityLevel{
		"critical":   schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL,
		"high":       schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_HIGH,
		"normal":     schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL,
		"low":        schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW,
		"background": schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_BACKGROUND,
	} {
		assert.Equal(t, priority{level: level}, priorityFromMetadata(md(name, "")), name)
	}
	assert.Equal(t, priority{}, priorityFromMetadata(md("urgent", "x")), "unknown values stay unset")
}
