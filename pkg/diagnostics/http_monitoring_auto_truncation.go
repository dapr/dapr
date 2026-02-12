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

package diagnostics

import (
	"strings"
)

// convertPathToMetricLabel removes the variant parameters in URL path for low cardinality label space
// For example, it removes {keys} param from /v1/state/statestore/{keys}.
// This is used for Low Cardinality metrics fallback
func (h *httpMetrics) convertPathToMetricLabel(path string) string {
	if path == "" {
		return path
	}

	path = NormalizeHTTPPath(path)

	p := path
	if p[0] == '/' {
		p = path[1:]
	}

	// Split up to 6 delimiters in 'v1/actors/DemoActor/1/timer/name'
	parsedPath := strings.SplitN(p, "/", 6)

	if len(parsedPath) < 3 {
		return path
	}

	// Replace actor id with {id} for appcallback url - 'actors/DemoActor/1/method/method1'
	if parsedPath[0] == "actors" {
		parsedPath[2] = "{id}"

		// actors/Type/id/method/methodName
		if len(parsedPath) > 3 && parsedPath[3] == "method" {
			// Keep method name (index 4), truncate rest
			if len(parsedPath) > 5 {
				return "/" + strings.Join(parsedPath[0:5], "/")
			}
		}

		return "/" + strings.Join(parsedPath, "/")
	}

	switch parsedPath[1] {
	case "state", "secrets":
		// state api: Concat 3 items(v1, state, statestore) in /v1/state/statestore/key
		// secrets api: Concat 3 items(v1, secrets, keyvault) in /v1/secrets/keyvault/name
		return "/" + strings.Join(parsedPath[0:3], "/")

	case "actors":
		if len(parsedPath) < 5 {
			return path
		}
		// ignore id part
		parsedPath[3] = "{id}"

		// /v1/actors/Type/id/method/methodName
		if parsedPath[4] == "method" {
			if len(parsedPath) > 5 {
				// We want to keep the method name, but truncate anything after it.
				// parsedPath[5] contains the rest of the path (because of SplitN(..., 6))
				// So we take the first segment of parsedPath[5] as the method name.
				parts := strings.SplitN(parsedPath[5], "/", 2)
				parsedPath[5] = parts[0]
				return "/" + strings.Join(parsedPath[0:6], "/")
			}
			return "/" + strings.Join(parsedPath, "/")
		}

		// For state, reminders, timers, truncate after the category (index 4)
		// Concat 5 items(v1, actors, DemoActor, {id}, state) in /v1/actors/DemoActor/1/state/key
		return "/" + strings.Join(parsedPath[0:5], "/")
	case "invoke":
		if len(parsedPath) < 5 {
			return path
		}
		// /v1.0/invoke/appId/method/methodName
		if parsedPath[3] == "method" && len(parsedPath) > 4 {
			// Return /v1.0/invoke/appId/method/methodName
			return "/" + strings.Join(parsedPath[0:5], "/")
		}
		return path
	case "workflows":
		if len(parsedPath) < 4 {
			return path
		}

		// v1.0-alpha1/workflows/<workflowComponentName>/<instanceId>
		if len(parsedPath) == 4 {
			parsedPath[3] = "{instanceId}"
			return "/" + strings.Join(parsedPath[0:4], "/")
		}

		// v1.0-alpha1/workflows/<workflowComponentName>/<workflowName>/start[?instanceID=<instanceID>]
		if len(parsedPath) == 5 && parsedPath[4] != "" && strings.HasPrefix(parsedPath[4], "start") {
			// not obfuscating the workflow name, just the possible instanceID
			return "/" + strings.Join(parsedPath[0:4], "/") + "/start"
		} else {
			// v1.0-alpha1/workflows/<workflowComponentName>/<instanceId>/terminate
			// v1.0-alpha1/workflows/<workflowComponentName>/<instanceId>/pause
			// v1.0-alpha1/workflows/<workflowComponentName>/<instanceId>/resume
			// v1.0-alpha1/workflows/<workflowComponentName>/<instanceId>/purge
			parsedPath[3] = "{instanceId}"
			// v1.0-alpha1/workflows/<workflowComponentName>/<instanceID>/raiseEvent/<eventName>
			if len(parsedPath) == 6 && parsedPath[4] == "raiseEvent" && parsedPath[5] != "" {
				parsedPath[5] = "{eventName}"
				return "/" + strings.Join(parsedPath[0:6], "/")
			}
		}
		return "/" + strings.Join(parsedPath[0:5], "/")
	}

	return path
}
