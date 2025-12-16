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
	"fmt"
	"path/filepath"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

func makeResourceCPUChart(ru ResourceUsage, prefix, outDir string) {
	p := plot.New()
	titleExtras := ""
	if ru.TargetRestarts > 0 || ru.TesterRestarts > 0 {
		titleExtras = fmt.Sprintf(" (restarts target=%d tester=%d)", ru.TargetRestarts, ru.TesterRestarts)
	}
	p.Title.Text = "Resource Usage – CPU (mCPU)" + titleExtras
	p.Y.Label.Text = "mCPU"
	values := plotter.Values{ru.AppCPUm, ru.SidecarCPUm}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = colorRGBA(255, 205, 86, 255) // yellow
	p.Add(bar)
	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "App"},
		{Value: 1, Label: "Sidecar"},
	})
	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_resource_cpu.png"))
}

func makeResourceMemChart(ru ResourceUsage, prefix, outDir string) {
	p := plot.New()
	titleExtras := ""
	if ru.TargetRestarts > 0 || ru.TesterRestarts > 0 {
		titleExtras = fmt.Sprintf(" (restarts target=%d tester=%d)", ru.TargetRestarts, ru.TesterRestarts)
	}
	p.Title.Text = "Resource Usage – Memory (MB)" + titleExtras
	p.Y.Label.Text = "MB"
	values := plotter.Values{ru.AppMemMB, ru.SidecarMemMB}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = colorRGBA(255, 99, 132, 255) // pinkish-red
	p.Add(bar)
	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "App"},
		{Value: 1, Label: "Sidecar"},
	})
	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_resource_memory.png"))
}
