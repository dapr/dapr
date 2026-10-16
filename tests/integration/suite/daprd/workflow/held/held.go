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

// Package held tests the WorkflowsHeldOperations preview: workflow operations
// aimed at an app with no workflow worker connected are held in the scheduler
// and replayed once a worker connects.
package held

import (
	"context"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
)

// heldKeys returns the scheduler keys of the held operations of instance id.
func heldKeys(t *testing.T, ctx context.Context, wf *workflow.Workflow, id api.InstanceID) []string {
	t.Helper()
	var keys []string
	for _, key := range wf.Scheduler().ListAllKeys(t, ctx, "dapr/jobs/actorreminder") {
		if strings.Contains(key, ".workflow||"+string(id)+"||held-") {
			keys = append(keys, key)
		}
	}
	return keys
}
