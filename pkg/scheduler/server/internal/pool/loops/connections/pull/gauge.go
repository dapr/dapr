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

import "github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"

// gauge counts pull triggers by app and activity name (the trigger's
// concurrency key) and publishes each count as it changes.
type gauge struct {
	record func(namespace, appID, activityName string, count int64)
	counts map[gaugeKey]int64
}

type gaugeKey struct {
	appID, name string
}

func newGauge(record func(namespace, appID, activityName string, count int64)) *gauge {
	return &gauge{record: record, counts: make(map[gaugeKey]int64)}
}

func (g *gauge) add(req *loops.TriggerRequest, delta int64) {
	meta := req.Job.GetMetadata()
	key := gaugeKey{appID: meta.GetAppId(), name: meta.GetConcurrencyKey()}
	g.counts[key] += delta
	g.record(meta.GetNamespace(), key.appID, key.name, g.counts[key])
	if g.counts[key] == 0 {
		delete(g.counts, key)
	}
}
