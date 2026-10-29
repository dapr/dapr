/*
Copyright 2024 The Dapr Authors
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

package serialize

import "strings"

// PrefixesFromNamespace returns key prefixes for all jobs types for a given
// namespace.
func PrefixesFromNamespace(namespace string) []string {
	return []string{
		"actorreminder||" + namespace + "||",
		"app||" + namespace + "||",
	}
}

// QueueStateKey returns the namespace and app ID a job is counted under in
// the cron queue state, which the scaling metrics report. Jobs API jobs count
// under their app. Reminders on a workflow or activity actor count under the
// app named in the actor type, not the app in the job metadata: an activity
// result reminder is created by the app that ran the activity but is
// delivered to the workflow's app. Every other job, including user actor
// reminders, is not counted.
func QueueStateKey(jobName string) (string, string, bool) {
	parts := strings.SplitN(jobName, "||", 4)
	if len(parts) != 4 {
		return "", "", false
	}

	namespace := parts[1]
	switch parts[0] {
	case "app":
		return namespace, parts[2], true

	case "actorreminder":
		appID, ok := strings.CutPrefix(parts[2], "dapr.internal."+namespace+".")
		if !ok {
			return "", "", false
		}
		for _, suffix := range []string{".workflow", ".activity"} {
			if app, ok := strings.CutSuffix(appID, suffix); ok && len(app) > 0 {
				return namespace, app, true
			}
		}
	}

	return "", "", false
}
