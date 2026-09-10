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

package main

import (
	"fmt"
	"sort"
	"strings"
)

// efficiencyRow captures throughput against the resources consumed to achieve
// it, so scenarios and versions can be compared on iterations/sec per CPU
// core and per GB of memory (app + sidecar combined).
type efficiencyRow struct {
	test         string
	itPerSec     float64
	appCPUm      float64
	appMemMB     float64
	sidecarCPUm  float64
	sidecarMemMB float64
}

// efficiencyByDir collects rows per chart output folder so the folder README
// can render a summary table next to the charts.
var efficiencyByDir = make(map[string][]efficiencyRow)

func recordEfficiency(dir, test string, agg Runner, ru *ResourceUsage) {
	if ru == nil || agg.Iterations.Values.Rate == 0 {
		return
	}
	efficiencyByDir[dir] = append(efficiencyByDir[dir], efficiencyRow{
		test:         test,
		itPerSec:     agg.Iterations.Values.Rate,
		appCPUm:      ru.AppCPUm,
		appMemMB:     ru.AppMemMB,
		sidecarCPUm:  ru.SidecarCPUm,
		sidecarMemMB: ru.SidecarMemMB,
	})
}

// writeEfficiencyTable renders the throughput-per-resource table for one
// chart folder. Per-core and per-GB figures divide throughput by the combined
// app + sidecar usage.
func writeEfficiencyTable(b *strings.Builder, rows []efficiencyRow) {
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].test < rows[j].test })
	b.WriteString("### Throughput per resource\n\n")
	b.WriteString("Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.\n")
	b.WriteString("Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.\n\n")
	b.WriteString("| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rows {
		totalCores := (r.appCPUm + r.sidecarCPUm) / 1000
		totalGB := (r.appMemMB + r.sidecarMemMB) / 1024
		perCore := "n/a"
		if totalCores > 0 {
			perCore = fmt.Sprintf("%.1f", r.itPerSec/totalCores)
		}
		perGB := "n/a"
		if totalGB > 0 {
			perGB = fmt.Sprintf("%.1f", r.itPerSec/totalGB)
		}
		fmt.Fprintf(b, "| %s | %.2f | %.0f | %.0f | %.0f | %.0f | %s | %s |\n",
			r.test, r.itPerSec, r.appCPUm, r.appMemMB, r.sidecarCPUm, r.sidecarMemMB, perCore, perGB)
	}
	b.WriteString("\n")
}
