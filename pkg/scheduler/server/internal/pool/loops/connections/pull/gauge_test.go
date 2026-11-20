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

package pull

import (
	"testing"

	"github.com/stretchr/testify/assert"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func TestGauge_SplitsActivitiesAndWorkflowTurns(t *testing.T) {
	t.Parallel()
	type rec struct {
		name  string
		count int64
	}
	var activities, workflows []rec
	g := newGauge(
		func(_, _, name, _ string, count int64) { activities = append(activities, rec{name, count}) },
		func(_, _, name, _ string, count int64) { workflows = append(workflows, rec{name, count}) },
	)
	trigger := func(actorType, name string) *loops.TriggerRequest {
		return &loops.TriggerRequest{Job: &internalsv1pb.JobEvent{Metadata: &schedulerv1pb.JobMetadata{
			AppId:          testAppID,
			ConcurrencyKey: &name,
			Target: &schedulerv1pb.JobTargetMetadata{Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: actorType, Id: "id"},
			}},
		}}}
	}

	g.add(trigger(testActorType, "act"), 1)
	g.add(trigger("dapr.internal.default.myapp.workflow", "wf"), 1)
	g.add(trigger("dapr.internal.default.myapp.workflow", "wf"), 1)
	g.add(trigger(testActorType, "act"), -1)

	assert.Equal(t, []rec{{"act", 1}, {"act", 0}}, activities)
	assert.Equal(t, []rec{{"wf", 1}, {"wf", 2}}, workflows)
	assert.Len(t, g.counts, 1, "a count back at zero is forgotten")
}
