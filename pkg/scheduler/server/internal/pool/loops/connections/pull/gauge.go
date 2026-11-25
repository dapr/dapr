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
	"strings"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// recordFn publishes a count for one app, name and priority level.
type recordFn func(namespace, appID, name, priorityLevel string, count int64)

// gauge counts pull triggers by app, name and priority level and publishes
// each count as it changes: activities on one metric, workflow turns on
// another.
type gauge struct {
	activity recordFn
	workflow recordFn
	counts   map[gaugeKey]int64
}

type gaugeKey struct {
	appID, name, level string
	workflow           bool
}

func newGauge(activity, workflow recordFn) *gauge {
	return &gauge{activity: activity, workflow: workflow, counts: make(map[gaugeKey]int64)}
}

func (g *gauge) add(req *loops.TriggerRequest, delta int64) {
	meta := req.Job.GetMetadata()
	key := gaugeKey{
		appID:    meta.GetAppId(),
		name:     meta.GetConcurrencyKey(),
		level:    levelName(levelOf(req)),
		workflow: isWorkflowType(meta.GetTarget().GetActor().GetType()),
	}
	g.counts[key] += delta
	record := g.activity
	if key.workflow {
		record = g.workflow
	}
	record(meta.GetNamespace(), key.appID, key.name, key.level, g.counts[key])
	if g.counts[key] == 0 {
		delete(g.counts, key)
	}
}

// isWorkflowType reports whether actorType is a workflow actor type, whose
// pull triggers are workflow turns.
func isWorkflowType(actorType string) bool {
	return strings.HasSuffix(actorType, ".workflow")
}
