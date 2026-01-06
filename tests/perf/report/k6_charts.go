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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

type Summary struct {
	Pass           bool     `json:"pass"`
	RunnersResults []Runner `json:"runnersResults"`
}

// K6 perf test summary for x iterations. Show success/failure/VUs.
func processK6Summary(k6JSON, testName, pkg, baseOutputDir string) {
	k6JSON = strings.TrimSpace(k6JSON)
	if k6JSON == "" || !strings.HasPrefix(k6JSON, "{") {
		return
	}

	var summary Summary
	if err := json.Unmarshal([]byte(k6JSON), &summary); err != nil {
		fmt.Fprintf(os.Stderr, "k6 JSON parse error for %s: %v\nJSON: %s\n", testName, err, k6JSON)
		return
	}
	if len(summary.RunnersResults) == 0 {
		return
	}

	// only one for k6 data
	r := summary.RunnersResults[0]
	info, ok := prepareOutputInfo(pkg, testName, baseOutputDir)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown API or transport for %s.\nk6 JSON: %+v\n", testName, summary)
		return
	}
	storeRunner(r, info)
}

// Data volume chart shows the total bytes received & sent
func makeDataVolumeChart(r Runner, prefix, outDir string) {
	p := plot.New()
	p.Title.Text = "Data (Payload+Headers) Volume (total)"

	const (
		bytesPerKB = 1024.0
		bytesPerMB = 1024.0 * 1024.0
		bytesPerGB = 1024.0 * 1024.0 * 1024.0
	)

	receivedBytes := r.DataReceived.Values.Count
	sentBytes := r.DataSent.Values.Count
	if receivedBytes == 0 && sentBytes == 0 {
		return
	}
	maxBytes := receivedBytes
	if sentBytes > maxBytes {
		maxBytes = sentBytes
	}

	unit := "MB"
	divisor := bytesPerMB
	if maxBytes < bytesPerMB {
		unit = "KB"
		divisor = bytesPerKB
	} else if maxBytes >= bytesPerGB {
		unit = "GB"
		divisor = bytesPerGB
	}
	p.Y.Label.Text = unit

	values := plotter.Values{
		receivedBytes / divisor,
		sentBytes / divisor,
	}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = colorRGBA(51, 153, 255, 255) // blue
	p.Add(bar)

	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: fmt.Sprintf("Data received (%s)", unit)},
		{Value: 1, Label: fmt.Sprintf("Data sent (%s)", unit)},
	})

	p.Y.Tick.Marker = plot.TickerFunc(func(min, max float64) []plot.Tick {
		def := plot.DefaultTicks{}
		defTicks := def.Ticks(min, max)
		for i := range defTicks {
			if defTicks[i].Label != "" {
				defTicks[i].Label = fmt.Sprintf("%.0f", defTicks[i].Value)
			}
		}
		return defTicks
	})

	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_data_volume.png"))
}
