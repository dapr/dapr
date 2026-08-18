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

// Package counters holds shared scheduler-job and metric counting helpers
// for the workflow scheduler fast-path integration suites.
package counters

import (
	"context"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
)

// JobCounts returns the number of new-event janitor backstop jobs and
// per-event new-event one-shot jobs currently in the scheduler.
func JobCounts(t *testing.T, ctx context.Context, s *procscheduler.Scheduler) (janitors, newEvents int) {
	t.Helper()
	for _, key := range s.ListAllKeys(t, ctx, "dapr/jobs") {
		if !strings.Contains(key, "new-event") {
			continue
		}
		if strings.Contains(key, "new-event-janitor") {
			janitors++
		} else {
			newEvents++
		}
	}
	return janitors, newEvents
}

// RunActivityJobCount returns the number of run-activity jobs currently in
// the scheduler.
func RunActivityJobCount(t *testing.T, ctx context.Context, s *procscheduler.Scheduler) int {
	t.Helper()
	var count int
	for _, key := range s.ListAllKeys(t, ctx, "dapr/jobs") {
		if strings.Contains(key, "run-activity") {
			count++
		}
	}
	return count
}

// LocalActivityStatusCount sums the local_activity metric series matching
// the given status.
func LocalActivityStatusCount(t *testing.T, ctx context.Context, d *daprd.Daprd, status string) float64 {
	t.Helper()
	return metricStatusCount(t, ctx, d, "local_activity", status)
}

// LocalWakeStatusCount sums the local_wake metric series matching the given
// status.
func LocalWakeStatusCount(t *testing.T, ctx context.Context, d *daprd.Daprd, status string) float64 {
	t.Helper()
	return metricStatusCount(t, ctx, d, "local_wake", status)
}

// FoldStatusCount sums the completions_fold metric series matching the given
// status.
func FoldStatusCount(t *testing.T, ctx context.Context, d *daprd.Daprd, status string) float64 {
	t.Helper()
	return metricStatusCount(t, ctx, d, "completions_fold", status)
}

func metricStatusCount(t *testing.T, ctx context.Context, d *daprd.Daprd, metric, status string) float64 {
	t.Helper()
	var count float64
	for k, v := range d.Metrics(t, ctx).All() {
		if strings.Contains(k, metric) && strings.Contains(k, status) {
			count += v
		}
	}
	return count
}

// FastPathFeatureConfig is the Configuration manifest enabling the
// WorkflowsFastPath preview feature, shared by every fast-path suite.
const FastPathFeatureConfig = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowsfastpath
spec:
  features:
  - name: WorkflowsFastPath
    enabled: true
`
