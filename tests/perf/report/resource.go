/*
Copyright 2025 The Dapr Authors
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

package main

import (
	"strconv"
	"strings"

	"github.com/dapr/dapr/tests/perf/markers"
)

type ResourceUsage struct {
	AppCPUm        float64
	AppMemMB       float64
	SidecarCPUm    float64
	SidecarMemMB   float64
	TargetRestarts int
	TesterRestarts int
	Drawn          bool
}

type parsedUsage struct {
	resource string  // app/sidecar
	cpuMilli float64 // mCPU
	memMB    float64
}

func parseResourceUsageLine(line string) (parsedUsage, bool) {
	l := strings.ToLower(strings.TrimSpace(line))
	var resource, marker string
	if idx := strings.Index(l, markers.TargetDaprAppConsumed); idx != -1 || strings.HasPrefix(l, markers.TargetDaprAppConsumed) {
		resource, marker = "app", markers.TargetDaprAppConsumed
	} else if idx := strings.Index(l, markers.TargetDaprConsumed); idx != -1 || strings.HasPrefix(l, markers.TargetDaprConsumed) {
		resource, marker = "sidecar", markers.TargetDaprConsumed
	} else {
		return parsedUsage{}, false
	}

	// rest after the marker
	markerPos := strings.Index(l, marker)
	if markerPos == -1 {
		return parsedUsage{}, false
	}
	rest := strings.TrimSpace(l[markerPos+len(marker):])

	// parse cpu: "<value>[m] cpu and ..."
	cpuMilli := 0.0
	if j := strings.Index(rest, " cpu"); j != -1 {
		cpuStr := strings.TrimSpace(rest[:j])
		if strings.HasSuffix(cpuStr, "m") {
			if v, err := strconv.ParseFloat(strings.TrimSuffix(cpuStr, "m"), 64); err == nil {
				cpuMilli = v
			}
		}
	}

	// parse mem: "... cpu and <value>mb of memory"
	memMB := 0.0
	if k := strings.Index(rest, " cpu and "); k != -1 {
		memPart := rest[k+len(" cpu and "):]
		if m := strings.Index(memPart, "mb of memory"); m != -1 {
			if v, err := strconv.ParseFloat(strings.TrimSpace(memPart[:m]), 64); err == nil {
				memMB = v
			}
		}
	}

	if cpuMilli == 0 && memMB == 0 {
		return parsedUsage{}, false
	}
	return parsedUsage{resource: resource, cpuMilli: cpuMilli, memMB: memMB}, true
}

type parsedRestarts struct {
	resource string // target/tester
	count    int
}

func parseRestartLine(line string) (parsedRestarts, bool) {
	l := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(l, "restarted") {
		return parsedRestarts{}, false
	}

	resource := ""
	if strings.Contains(l, markers.TargetDaprAppRestarted) || strings.HasPrefix(l, markers.TargetDaprAppRestarted) {
		resource = "target"
	} else if strings.Contains(l, markers.TargetTesterAppRestarted) || strings.HasPrefix(l, markers.TargetDaprAppRestarted) {
		resource = "tester"
	} else {
		return parsedRestarts{}, false
	}

	// get val btw "restarted " && " times"
	count := 0
	if idx := strings.Index(l, "restarted "); idx != -1 {
		rest := l[idx+len("restarted "):]
		if j := strings.Index(rest, " times"); j != -1 {
			numStr := strings.TrimSpace(rest[:j])
			if v, err := strconv.Atoi(numStr); err == nil {
				count = v
			}
		}
	}
	return parsedRestarts{resource: resource, count: count}, true
}
