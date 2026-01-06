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
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

// Duration comparison (p50/Med & p95) across multiple runs of the same test.
// Show as 2 lines (median & p95) on the chart
func makeCombinedCharts(runners []Runner, prefix, outDir string) {
	// If there's only 1 run, there's nothing to compare -> skip this chart
	if len(runners) < 2 {
		return
	}

	p := plot.New()
	p.Title.Text = fmt.Sprintf("HTTP Request Duration – %d Runs Compared", len(runners))
	p.Y.Label.Text = "Latency (ms)"
	p.X.Label.Text = "Run"

	medPts := make(plotter.XYs, len(runners))
	p95Pts := make(plotter.XYs, len(runners))
	ticks := make([]plot.Tick, len(runners))

	for i, r := range runners {
		x := float64(i + 1)
		medMs := r.HTTPReqDuration.Values.Med * 1000
		p95Ms := r.HTTPReqDuration.Values.P95 * 1000

		medPts[i].X = x     // put the med point here
		medPts[i].Y = medMs // at this height (ms)
		p95Pts[i].X = x
		p95Pts[i].Y = p95Ms

		ticks[i] = plot.Tick{Value: x, Label: fmt.Sprintf("Run %d", i+1)}
	}

	medLine, _ := plotter.NewLine(medPts)
	medLine.Color = colorRGBA(100, 200, 100, 255) // green
	medLine.Width = vg.Points(2)

	p95Line, _ := plotter.NewLine(p95Pts)
	p95Line.Color = colorRGBA(255, 100, 100, 255) // red
	p95Line.Width = vg.Points(2)

	p.Add(medLine, p95Line)
	p.Legend.Add("p50 (median)", medLine)
	p.Legend.Add("p95", p95Line)
	p.Legend.Top = true

	p.X.Tick.Marker = plot.ConstantTicks(ticks)
	p.X.Min = 0.5
	p.X.Max = float64(len(runners)) + 0.5

	// Force int formatting on y-axis tick labels for consistency across charts
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

	p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_duration_comparison.png"))
}

// Make duration breakdown chart for both full range and low range. Full range shows all, but is harder to
// see the low-end lines, so made a low range chart to highlight the lower end lines clearly by rm-ing the
// higher lines and adjusting the y-axis scope to be the low-end max.
func makeDurationBreakdownChart(r Runner, prefix, outDir string) {
	// Full-range chart
	full := plot.New()
	full.Title.Text = "HTTP Request Duration Breakdown (full range)"
	full.X.Label.Text = "Percentile"
	full.Y.Label.Text = "Time (seconds)"

	hasP99 := r.HTTPReqDuration.Values.P99 != 0
	hasP999 := r.HTTPReqDuration.Values.P999 != 0

	type field struct {
		label string
		get   func(v Values) float64
	}
	fields := []field{
		{"min", func(v Values) float64 { return v.Min }},
		{"med", func(v Values) float64 { return v.Med }},
		{"avg", func(v Values) float64 { return v.Avg }},
		{"p90", func(v Values) float64 { return v.P90 }},
		{"p95", func(v Values) float64 { return v.P95 }},
	}
	if hasP99 {
		fields = append(fields, field{"p99", func(v Values) float64 { return v.P99 }})
	}
	if hasP999 {
		fields = append(fields, field{"p99.9", func(v Values) float64 { return v.P999 }})
	}
	fields = append(fields, field{"max", func(v Values) float64 { return v.Max }})

	// x-axis positions & ticks based on fields
	xPos := make([]float64, len(fields))
	ticks := make([]plot.Tick, len(fields))
	for i, f := range fields {
		x := float64(i + 1)
		xPos[i] = x
		ticks[i] = plot.Tick{Value: x, Label: f.label}
	}

	type lineDef struct {
		name   string
		metric Metric
		color  color.RGBA
	}
	lines := []lineDef{
		{"Connecting", r.HTTPReqConnecting, colorRGBA(255, 99, 132, 255)},          // pinkish-red
		{"TLS", r.HTTPReqTLS, colorRGBA(54, 162, 235, 255)},                        // lighter blue
		{"Sending", r.HTTPReqSending, colorRGBA(255, 206, 86, 255)},                // yellow
		{"Receiving", r.HTTPReqReceiving, colorRGBA(153, 102, 255, 255)},           // purple
		{"Blocked", r.HTTPReqBlocked, colorRGBA(255, 159, 64, 255)},                // orange
		{"Waiting", r.HTTPReqWaiting, colorRGBA(75, 192, 192, 255)},                // green blue
		{"Duration", r.HTTPReqDuration, colorRGBA(0, 114, 178, 255)},               // darker blue
		{"Iteration Duration", r.IterationDuration, colorRGBA(102, 194, 165, 255)}, // greener blue
		{"Failed Requests", r.HTTPReqFailed, colorRGBA(220, 53, 69, 255)},          // red
	}

	// Build the low latency charts -> exclude the large metrics
	lowLines := make([]lineDef, 0, len(lines))
	for _, ld := range lines {
		// skip these bc they are generally higher and make the lower end of the chart harder to see clearly
		if ld.name == "Waiting" || ld.name == "Duration" || ld.name == "Iteration Duration" {
			continue
		}
		lowLines = append(lowLines, ld)
	}

	// Determine appropriate time unit scale based on the largest value across all series
	maxFullSeconds := 0.0
	for _, ld := range lines {
		v := ld.metric.Values
		for i := range fields {
			val := fields[i].get(v)
			if val > maxFullSeconds {
				maxFullSeconds = val
			}
		}
	}
	// adjust units accordingly
	unit := "seconds"
	scale := 1.0
	if maxFullSeconds < 0.001 { // < 1 ms then use microseconds
		unit = "µs"
		scale = 1e6
	} else if maxFullSeconds < 1.0 { // < 1 s then use milliseconds
		unit = "ms"
		scale = 1e3
	}
	full.Y.Label.Text = fmt.Sprintf("Time (%s)", unit)

	// low chart max -ignore last field which is max
	lowMaxSeconds := 0.0
	for _, ld := range lowLines {
		v := ld.metric.Values
		for i := range fields[:len(fields)-1] {
			val := fields[i].get(v)
			if val > lowMaxSeconds {
				lowMaxSeconds = val
			}
		}
	}

	// Low-latency chart
	low := plot.New()
	low.Title.Text = "HTTP Request Duration – Low Latency (zoom)"
	low.X.Label.Text = "Percentile"
	low.Y.Label.Text = fmt.Sprintf("Time (%s)", unit)
	low.Y.Min = 0
	if lowMaxSeconds == 0 {
		low.Y.Max = 1
	} else {
		low.Y.Max = lowMaxSeconds * scale * 1.1
	}
	low.Y.Tick.Marker = plot.TickerFunc(func(min, max float64) []plot.Tick {
		def := plot.DefaultTicks{}
		defTicks := def.Ticks(min, max)
		for i := range defTicks {
			if defTicks[i].Label != "" {
				defTicks[i].Label = fmt.Sprintf("%.0f", defTicks[i].Value)
			}
		}
		return defTicks
	})

	isAllZero := func(m Metric) bool {
		return m.Values.Min == 0 &&
			m.Values.Med == 0 &&
			m.Values.Avg == 0 &&
			m.Values.P90 == 0 &&
			m.Values.P95 == 0 &&
			m.Values.Max == 0
	}

	anyPlottedFull := false
	anyPlottedLow := false

	// build series from lines
	buildSeries := func(defs []lineDef) ([][]float64, []string, []color.RGBA) {
		var series [][]float64
		var names []string
		var colorsSlice []color.RGBA
		for _, ld := range defs {
			if isAllZero(ld.metric) {
				continue
			}
			y := make([]float64, len(fields))
			for i := range fields {
				y[i] = fields[i].get(ld.metric.Values) * scale
			}
			series = append(series, y)
			names = append(names, ld.name)
			colorsSlice = append(colorsSlice, ld.color)
		}
		return series, names, colorsSlice
	}

	// Full chart
	if series, legName, colorsSlice := buildSeries(lines); len(series) > 0 {
		plotLines(full, series, xPos, legName, colorsSlice)
		anyPlottedFull = true
	}
	// Low chart
	if series, legName, colorsSlice := buildSeries(lowLines); len(series) > 0 {
		plotLines(low, series, xPos, legName, colorsSlice)
		anyPlottedLow = true
	}

	full.X.Tick.Marker = plot.ConstantTicks(ticks)
	low.X.Tick.Marker = plot.ConstantTicks(ticks)
	full.Legend.Top = true
	low.Legend.Top = true

	if anyPlottedFull {
		full.Save(12*vg.Inch, 5*vg.Inch, filepath.Join(outDir, prefix+"_duration_breakdown.png"))
	}
	if anyPlottedLow {
		low.Save(12*vg.Inch, 5*vg.Inch, filepath.Join(outDir, prefix+"_duration_low.png"))
	}
}

// draw multiple lines/series on the plot, using shared X positions
func plotLines(p *plot.Plot, series [][]float64, x []float64, names []string, colors []color.RGBA) {
	if p == nil || len(series) == 0 || len(x) == 0 {
		return
	}
	for si, ys := range series {
		if len(ys) != len(x) {
			continue
		}
		pts := make(plotter.XYs, len(x))
		for i := range x {
			pts[i].X = x[i]
			pts[i].Y = ys[i]
		}
		line, err := plotter.NewLine(pts)
		if err != nil {
			continue
		}
		if si < len(colors) {
			line.Color = colors[si]
		}
		line.Width = vg.Points(3)
		p.Add(line)
		if si < len(names) && names[si] != "" { // legend names
			p.Legend.Add(names[si], line)
		}
	}
	p.Legend.Top = true
}

func makeTailLatencyChart(r Runner, prefix, outDir string) {
	if r.HTTPReqDuration.IsZero() {
		return
	}
	p := plot.New()
	p.Title.Text = "Tail Latency Detail (p90 -> p99.9)"
	p.Y.Label.Text = "Latency (ms)"
	p.X.Label.Text = "Percentile"

	pts := plotter.XYs{
		{X: 1, Y: r.HTTPReqDuration.Values.P90 * 1000},
		{X: 2, Y: r.HTTPReqDuration.Values.P95 * 1000},
		{X: 3, Y: r.HTTPReqDuration.Values.P99 * 1000},
		{X: 4, Y: r.HTTPReqDuration.Values.P999 * 1000},
		{X: 5, Y: r.HTTPReqDuration.Values.Max * 1000},
	}
	line, _ := plotter.NewLine(pts)
	line.Color = colorRGBA(220, 53, 69, 255) // red
	line.Width = vg.Points(4)
	p.Add(line)

	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 1, Label: "p90"},
		{Value: 2, Label: "p95"},
		{Value: 3, Label: "p99"},
		{Value: 4, Label: "p99.9"},
		{Value: 5, Label: "max"},
	})
	p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_tail_latency.png"))
}

// Summary chart showing success/failed/VUs.
func makeSummaryChart(r Runner, prefix, outDir string) {
	p := plot.New()
	iterations := r.Iterations.Values.Count
	p.Title.Text = fmt.Sprintf("Performance Test Summary for %d Iterations", int(iterations))
	p.Y.Label.Text = "Value"

	successPct := r.Checks.Values.Rate * 100.0
	failedPct := (1.0 - r.Checks.Values.Rate) * 100.0

	vus := r.VUsMax.Values.Max
	values := plotter.Values{successPct, failedPct, vus}

	// bar width
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = colorRGBA(54, 162, 235, 255) // blue

	p.Add(bar)

	// set below vals to have a set x-axis otherwise the spacing is weird (makes them centered)
	p.X.Min = -0.5
	p.X.Max = float64(len(values)) - 0.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "Success (%)"},
		{Value: 1, Label: "Failed (%)"},
		{Value: 2, Label: "VUs"},
	})

	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_summary.png"))
}

// Throughput chart to chart the data received & sent (KB/s)
func makeThroughputChart(r Runner, prefix, outDir string) {
	p := plot.New()
	iterRate := r.Iterations.Values.Rate
	if iterRate < 0 {
		iterRate = 0
	}
	p.Title.Text = fmt.Sprintf("Data (Payload+Headers) Throughput (KB/s) – %.2f iterations/sec", iterRate)
	p.Y.Label.Text = "KB/s"

	recvKBs := r.DataReceived.Values.Rate / 1024 // KB/s
	sentKBs := r.DataSent.Values.Rate / 1024     // KB/s
	if recvKBs == 0 && sentKBs == 0 {
		return
	}
	values := plotter.Values{recvKBs, sentKBs}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = colorRGBA(51, 153, 255, 255) // blue
	p.Add(bar)

	// set below vals to have a set x-axis otherwise the spacing is weird (makes them centered)
	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "Data received (KB/s)"},
		{Value: 1, Label: "Data sent (KB/s)"},
	})

	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_throughput.png"))
}

// side-by-side comparison chart for pubsub variants
func makeVariantComparisonCharts(labels []string, runners []Runner, basePrefix, outDir string) {
	if len(labels) == 0 || len(labels) != len(runners) {
		return
	}
	// make series only for percentiles that are present
	type series struct {
		name   string
		color  color.RGBA
		values plotter.Values
	}
	// values (ms)
	medVals := make(plotter.Values, len(runners))
	p75Vals := make(plotter.Values, len(runners))
	p90Vals := make(plotter.Values, len(runners))
	p95Vals := make(plotter.Values, len(runners))
	p99Vals := make(plotter.Values, len(runners))
	p999Vals := make(plotter.Values, len(runners))
	hasP75, hasP90, hasP95, hasP99, hasP999 := false, false, false, false, false
	for i, r := range runners {
		v := r.HTTPReqDuration.Values
		medVals[i] = v.Med * 1000
		p75Vals[i] = v.P75 * 1000
		p90Vals[i] = v.P90 * 1000
		p95Vals[i] = v.P95 * 1000
		p99Vals[i] = v.P99 * 1000
		p999Vals[i] = v.P999 * 1000
		if v.P75 > 0 {
			hasP75 = true
		}
		if v.P90 > 0 {
			hasP90 = true
		}
		if v.P95 > 0 {
			hasP95 = true
		}
		if v.P99 > 0 {
			hasP99 = true
		}
		if v.P999 > 0 {
			hasP999 = true
		}
	}
	// Decide which series to plot
	seriesList := []series{
		{"p50 (median)", colorRGBA(100, 200, 100, 255), medVals}, // green
	}
	if hasP75 {
		seriesList = append(seriesList, series{"p75", colorRGBA(153, 102, 255, 255), p75Vals}) // purple
	}
	if hasP90 {
		seriesList = append(seriesList, series{"p90", colorRGBA(75, 192, 192, 255), p90Vals}) // light blueish-greenish
	}
	if hasP95 {
		seriesList = append(seriesList, series{"p95", colorRGBA(54, 162, 235, 255), p95Vals}) // darker blue
	}
	if hasP99 {
		seriesList = append(seriesList, series{"p99", colorRGBA(255, 159, 64, 255), p99Vals}) // orange
	}
	if hasP999 {
		seriesList = append(seriesList, series{"p99.9", colorRGBA(255, 99, 132, 255), p999Vals}) // pinkish-red
	}

	p := plot.New()
	p.Title.Text = "PubSub Variants – HTTP Request Duration"
	p.Y.Label.Text = "Latency (ms)"
	ticks := make([]plot.Tick, len(runners))
	for i := range runners {
		ticks[i] = plot.Tick{Value: float64(i), Label: labels[i]}
	}
	// Add grouped bars with offsets for nicer spacing, otherwise spacing is weird
	barW := vg.Points(10)
	offsetStart := -vg.Points(float64(len(seriesList)-1)) * barW / 2
	for si, s := range seriesList {
		bar, _ := plotter.NewBarChart(s.values, barW)
		bar.Color = s.color
		bar.Offset = offsetStart + vg.Points(float64(si))*barW
		p.Add(bar)
		p.Legend.Add(s.name, bar)
	}
	p.Legend.Top = true
	p.X.Min = -0.5
	p.X.Max = float64(len(runners)) - 0.5
	p.X.Tick.Marker = plot.ConstantTicks(ticks)
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
	p.Save(16*vg.Inch, 5*vg.Inch, filepath.Join(outDir, basePrefix+"_variants_duration.png"))
}

type outputInfo struct {
	apiName   string
	transport string
	outDir    string
	groupKey  string
	isPubsub  bool

	// pubsub comparison fields
	compKey  string
	baseFunc string
	label    string
}

// Resolve api/transport, output dir, grouping key, and the comparison aggregation keys for pubsub
func prepareOutputInfo(pkg, testName, baseOutputDir string) (outputInfo, bool) {
	apiName, transport, ok := classifyAPIAndTransport(pkg, testName)
	if !ok {
		return outputInfo{}, false
	}
	// put pubsub "bulk" into pubsub/bulk subfolder
	if strings.HasPrefix(apiName, "pubsub") && strings.Contains(strings.ToLower(testName), "bulk") {
		apiName = "pubsub/bulk"
	}
	outDir := filepath.Join(baseOutputDir, apiName)
	if transport != "" {
		outDir = filepath.Join(outDir, transport)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating charts directory %s: %v\n", outDir, err)
		return outputInfo{}, false
	}

	var groupKey string
	isPubsub := strings.HasPrefix(apiName, "pubsub")
	// Each parameterized variant aggregates across its own runs
	// ex: TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)
	// vs  TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal
	// vs  TestConfigurationSubscribeGRPCPerformance for non pubsub API
	// vs  TestWorkflowWithDifferentPayloads for wf API
	if isPubsub {
		groupKey = sanitizeName(stripRunOrdinalSuffix(testName))
	} else {
		base := testName
		if i := strings.Index(base, "/"); i != -1 {
			base = base[:i]
		}
		base = stripRunOrdinalSuffix(base)
		groupKey = sanitizeName(base)
	}

	output := outputInfo{
		apiName:   apiName,
		transport: transport,
		outDir:    outDir,
		groupKey:  groupKey,
		isPubsub:  isPubsub,
	}

	// comparison aggregation keys
	if isPubsub {
		baseFunc := testName
		if idx := strings.Index(baseFunc, "/"); idx != -1 {
			baseFunc = baseFunc[:idx]
		}
		if i := strings.Index(baseFunc, ":_"); i != -1 {
			baseFunc = baseFunc[:i]
		}
		var label string
		if idx := strings.Index(testName, "/"); idx != -1 {
			label = sanitizeName(testName[idx+1:])
		} else {
			label = sanitizeName(testName)
		}
		compKey := apiName + "/"
		if transport != "" {
			compKey += transport + "/"
		}
		compKey += sanitizeName(baseFunc)
		output.baseFunc = baseFunc
		output.label = label
		output.compKey = compKey
	}
	return output, true
}

// storeRunner aggregates the runner into combinedResults & for pubsub the comparison sets
func storeRunner(r Runner, output outputInfo) {
	// combine results per API/transport/groupKey
	mapKey := output.apiName + "/"
	if output.transport != "" {
		mapKey += output.transport + "/"
	}
	mapKey += output.groupKey
	result, exists := combinedResults[mapKey]
	if !exists {
		result = combinedTestResult{
			name:    output.groupKey,
			outDir:  output.outDir,
			runners: []Runner{},
		}
	}
	result.runners = append(result.runners, r)
	combinedResults[mapKey] = result
	if debugEnabled {
		debugf("storeRunner result: %+v", result)
	}

	// Pubsub comparison chart accumulation across parameterized variants
	// ex: TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_breakdown.png
	// vs  TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_breakdown.png
	// used for the *_variants_duration.png charts
	if output.isPubsub {
		cmp := pubsubComparisons[output.compKey]
		if cmp.baseName == "" {
			cmp.baseName = output.baseFunc
			cmp.outDir = output.outDir
		}
		cmp.labels = append(cmp.labels, output.label)
		cmp.runners = append(cmp.runners, r)
		pubsubComparisons[output.compKey] = cmp
		if debugEnabled {
			debugf("pubsubComparisons: %+v", pubsubComparisons)
		}
	}
	if debugEnabled {
		debugf("outputInfo: %+v", output)
	}
}

func colorRGBA(r, g, b, a uint8) color.RGBA {
	return color.RGBA{R: r, G: g, B: b, A: a}
}
