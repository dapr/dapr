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

	"github.com/dapr/dapr/pkg/config"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

func TestSentPriority(t *testing.T) {
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
	priorityFromMetadata := func(md map[string]*internalsv1pb.ListStringValue) priority {
		level, weight := sentPriority(md)
		return priority{level: priorityLevels[level], weight: weight}
	}

	assert.Equal(t, priority{}, priorityFromMetadata(nil), "no metadata leaves both unset")
	assert.Equal(t, priority{}, priorityFromMetadata(md("", "")))
	assert.Equal(t, priority{level: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL, weight: 3}, priorityFromMetadata(md("critical", "3")))
	assert.Equal(t, priority{weight: 7}, priorityFromMetadata(md("", "7")))
	assert.Equal(t, priority{weight: 1000}, priorityFromMetadata(md("", "1000")))
	assert.Equal(t, priority{}, priorityFromMetadata(md("", "1001")), "a weight above 1000 stays unset")
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

func TestPriorityOf(t *testing.T) {
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
	critical := schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL

	f := &factory{}
	assert.Equal(t, priority{level: critical, weight: 6}, f.priorityOf(md("critical", "6"), new("act")), "no resolver: what was sent")
	assert.Equal(t, priority{}, f.priorityOf(nil, new("act")))

	spec := &config.WorkflowSpec{Priority: &config.WorkflowPriority{
		Default: &config.PriorityValues{PriorityLevel: new(config.PriorityLevelLow), FairnessWeight: new(uint32(2))},
		Activities: []config.NamedPriorityValues{
			{Name: new("low-only"), PriorityLevel: new(config.PriorityLevelLow)},
			{Name: new("weight-only"), FairnessWeight: new(uint32(3))},
			{Name: new("both"), PriorityLevel: new(config.PriorityLevelBackground), FairnessWeight: new(uint32(5))},
		},
	}}
	f = &factory{receivedPriority: spec.ActivityReceivedPriority}
	for _, tc := range []struct {
		activity, level, weight string
		want                    priority
		msg                     string
	}{
		{"act", "critical", "6", priority{level: critical, weight: 6}, "no entry: what was sent beats this app's default"},
		{"act", "", "", priority{level: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW, weight: 2}, "nothing sent: this app's default"},
		{"act", "critical", "", priority{level: critical, weight: 2}, "sent level, default weight"},
		{"low-only", "critical", "6", priority{level: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW, weight: 6}, "own level beats the sent level"},
		{"weight-only", "critical", "6", priority{level: critical, weight: 3}, "own weight beats the sent weight"},
		{"both", "critical", "6", priority{level: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_BACKGROUND, weight: 5}, "own entry beats both"},
		{"act", "urgent", "5000", priority{level: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW, weight: 2}, "invalid sent values fall to this app's default"},
	} {
		assert.Equal(t, tc.want, f.priorityOf(md(tc.level, tc.weight), new(tc.activity)), tc.msg)
	}
	assert.Equal(t, priority{level: critical, weight: 6}, f.priorityOf(md("critical", "6"), nil), "no name: what was sent")
}
