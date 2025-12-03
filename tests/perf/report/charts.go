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
	"bufio"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

// Collect all runs of the same test for aggregates when more than 1 iteration is run
type combinedTestResult struct {
	name    string
	outDir  string
	runners []Runner
}

var combinedResults = make(map[string]combinedTestResult)

type Values struct {
	Min   float64 `json:"min"`
	Med   float64 `json:"med"`
	Avg   float64 `json:"avg"`
	P90   float64 `json:"p(90)"`
	P95   float64 `json:"p(95)"`
	Max   float64 `json:"max"`
	Rate  float64 `json:"rate,omitempty"`
	Count float64 `json:"count,omitempty"`
}

type Metric struct {
	Values Values `json:"values"`
}

type Runner struct {
	HTTPReqDuration   Metric `json:"http_req_duration"`
	HTTPReqWaiting    Metric `json:"http_req_waiting"`
	HTTPReqConnecting Metric `json:"http_req_connecting"`
	HTTPReqTLS        Metric `json:"http_req_tls_handshaking"`
	HTTPReqSending    Metric `json:"http_req_sending"`
	HTTPReqReceiving  Metric `json:"http_req_receiving"`
	HTTPReqBlocked    Metric `json:"http_req_blocked"`
	HTTPReqFailed     Metric `json:"http_req_failed"`
	IterationDuration Metric `json:"iteration_duration"`
	Iterations        Metric `json:"iterations"`
	Checks            Metric `json:"checks"`
	VUsMax            Metric `json:"vus_max"`
	DataReceived      Metric `json:"data_received"`
	DataSent          Metric `json:"data_sent"`
}

type Summary struct {
	Pass           bool     `json:"pass"`
	RunnersResults []Runner `json:"runnersResults"`
}
type goTestEvent struct {
	Time    string `json:"Time"`
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test,omitempty"`
	Output  string `json:"Output,omitempty"`
}

const (
	workflowsPerfPkg = "github.com/dapr/dapr/tests/perf/workflows"

	// on the first line of the k6 JSON summary, signals the start of the data
	k6SummaryMarker = "test summary `"
	inputPath       = "./test_report_perf.json"
)

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer(
		"/", "_",
		" ", "_",
		"[", "",
		"]", "",
		"=", "",
		",", "",
	)
	return replacer.Replace(s)
}

// create charts based on perf json output
func main() {
	f, err := os.Open(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening %s: %v\n", inputPath, err)
		os.Exit(1)
	}
	defer f.Close()

	// TODO Cassie: parameterize the dapr version based on CI input based on the version
	// TODO Cassie: expand beyond workflows API
	outputDir := filepath.Join("charts", "v1.16.3", "workflows")
	if err = os.MkdirAll(outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating charts directory %s: %v\n", outputDir, err)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(f)
	var (
		collecting  bool
		currentTest string
		jsonBuilder strings.Builder
		generated   int
	)

	for scanner.Scan() {
		var ev goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			// bad line in the json — just skip it...
			continue
		}

		// For now we only care about the workflows perf pkg.
		// TODO Cassie: extend in the future to other APIs
		if ev.Package != workflowsPerfPkg {
			continue
		}

		switch ev.Action {
		// Marks the start of a subtest. Reset any partial json we may
		// have been collecting for a previous test
		case "run":
			if ev.Test != "" {
				collecting = false
				currentTest = ev.Test
				jsonBuilder.Reset()
			}

		// Only consider output that belongs to the current running test
		case "output":
			if ev.Test == "" || ev.Test != currentTest {
				continue
			}

			// If we are already collecting JSON for a test, keep appending
			// chunks until we see the terminating backtick `
			if collecting {
				if idx := strings.Index(ev.Output, "`"); idx != -1 {
					// Final chunk = everything before the backtick.
					chunk := strings.TrimSpace(ev.Output[:idx])
					if chunk != "" {
						jsonBuilder.WriteString("\n")
						jsonBuilder.WriteString(chunk)
					}

					k6JSON := jsonBuilder.String()
					processSummary(k6JSON, currentTest, outputDir)
					generated++

					collecting = false
					jsonBuilder.Reset()
					continue
				}

				// Middle chunk: append whole line.
				// ex: ... \"iterations\": {\n"}
				chunk := strings.TrimSpace(ev.Output)
				if chunk != "" {
					jsonBuilder.WriteString("\n")
					jsonBuilder.WriteString(chunk)
				}
				continue
			}

			// Not collecting atm, so look for the marker that starts the JSON we care about
			markerIdx := strings.Index(ev.Output, k6SummaryMarker)
			if markerIdx == -1 {
				// not present
				continue
			}

			afterMarker := ev.Output[markerIdx+len(k6SummaryMarker):]

			// Start of a multi-line json block. Save first chunk (which begins
			// with "{") and continue collecting on subsequent lines.
			// ex: {\n"} before "iterations"
			collecting = true
			jsonBuilder.Reset()
			firstChunk := strings.TrimSpace(afterMarker)
			if firstChunk != "" {
				jsonBuilder.WriteString(firstChunk)
			}

		// If this is the package-level completion event (no Test field), then
		// we know we've reached the end of all API specific (workflows) perf tests
		// ex: {"Time":"...","Action":"pass","Package":"github.com/dapr/dapr/tests/perf/workflows","Elapsed":...}
		case "pass", "fail", "skip":
			if ev.Test == "" {
				if collecting {
					fmt.Fprintf(os.Stderr, "Package %s completed while still collecting k6 JSON for test %s\n", workflowsPerfPkg, currentTest)
					collecting = false
					jsonBuilder.Reset()
				}
				// Future work we may want to process like additional packages from the same file
				continue
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "scanner error: %v\n", err)
		os.Exit(1)
	}

	// create aggregate + comparison charts per test
	for _, result := range combinedResults {
		runCount := len(result.runners)
		if runCount == 0 {
			continue
		}

		// aggregate results if more than 1 iteration ran
		// for ex: TestWorkflowWithConstantVUs runs 3 times
		prefix := result.name
		if runCount > 1 {
			prefix = result.name + "_avg"
		}

		agg := aggregateRunners(result.runners)

		// create charts
		makeDurationBreakdownChart(agg, prefix, result.outDir)
		makeSummaryChart(agg, prefix, result.outDir)
		makeThroughputChart(agg, prefix, result.outDir)
		makeDataVolumeChart(agg, prefix, result.outDir)

		// comparison chart across runs (skipped if only 1 run)
		makeCombinedCharts(result.runners, result.name, result.outDir)
	}

	fmt.Printf("Generated charts for performance tests in %s\n", outputDir)
}

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

	var sumMedMs, sumP95Ms float64
	for i, r := range runners {
		x := float64(i + 1)
		medMs := r.HTTPReqDuration.Values.Med * 1000
		p95Ms := r.HTTPReqDuration.Values.P95 * 1000

		medPts[i].X = x     // put the med point here
		medPts[i].Y = medMs // at this height (ms)
		p95Pts[i].X = x
		p95Pts[i].Y = p95Ms

		sumMedMs += medMs
		sumP95Ms += p95Ms
		ticks[i] = plot.Tick{Value: x, Label: fmt.Sprintf("Run %d", i+1)}
	}

	medLine, _ := plotter.NewLine(medPts)
	medLine.Color = color.RGBA{100, 200, 100, 255} // green
	medLine.Width = vg.Points(2)

	p95Line, _ := plotter.NewLine(p95Pts)
	p95Line.Color = color.RGBA{255, 100, 100, 255} // red
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

// Perf test summary for x iterations. Show success/failure/VUs.
func processSummary(k6JSON, testName, outputDir string) {
	k6JSON = strings.TrimSpace(k6JSON)
	if k6JSON == "" || !strings.HasPrefix(k6JSON, "{") {
		return
	}

	var summary Summary
	if err := json.Unmarshal([]byte(k6JSON), &summary); err != nil {
		fmt.Fprintf(os.Stderr, "JSON parse error for %s: %v\nJSON: %s\n", testName, err, k6JSON)
		return
	}
	if len(summary.RunnersResults) == 0 {
		return
	}

	r := summary.RunnersResults[0]

	// Group multiple runs of the same test by stripping the
	// subtest suffixes. ex: ":_" && ":_#02", so we end up with 1 key.
	// ex: TestWorkflowWithConstantVUs/[T_30_300]:_ && TestWorkflowWithConstantVUs/[T_30_300]:_#01
	baseName := testName
	if i := strings.Index(baseName, ":_"); i != -1 {
		baseName = baseName[:i]
	}
	key := sanitizeName(baseName)

	result, exists := combinedResults[key]
	if !exists {
		result = combinedTestResult{
			name:    key,
			outDir:  outputDir,
			runners: []Runner{},
		}
	}
	result.runners = append(result.runners, r)
	combinedResults[key] = result
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

	xPos := []float64{1, 2, 3, 4, 5, 6}

	type lineDef struct {
		name   string
		values []float64
		color  color.RGBA
	}
	lines := []lineDef{
		{"Connecting", vals(r.HTTPReqConnecting), color.RGBA{255, 99, 132, 255}},          // pinkish-red
		{"TLS", vals(r.HTTPReqTLS), color.RGBA{54, 162, 235, 255}},                        // lighter blue
		{"Sending", vals(r.HTTPReqSending), color.RGBA{255, 206, 86, 255}},                // yellow
		{"Receiving", vals(r.HTTPReqReceiving), color.RGBA{153, 102, 255, 255}},           // purple
		{"Blocked", vals(r.HTTPReqBlocked), color.RGBA{255, 159, 64, 255}},                // orange
		{"Waiting", vals(r.HTTPReqWaiting), color.RGBA{75, 192, 192, 255}},                // green blue
		{"Duration", vals(r.HTTPReqDuration), color.RGBA{0, 114, 178, 255}},               // darker blue
		{"Iteration Duration", vals(r.IterationDuration), color.RGBA{102, 194, 165, 255}}, // greener blue
		{"Failed Requests", vals(r.HTTPReqFailed), color.RGBA{220, 53, 69, 255}},          // red
	}

	// Build the low latency charts -> exclude the large metrics
	var lowLines []lineDef
	for _, ld := range lines {
		// skip these bc they are generally higher and make the lower end of the chart harder to see clearly
		if ld.name == "Waiting" || ld.name == "Duration" || ld.name == "Iteration Duration" {
			continue
		}
		lowLines = append(lowLines, ld)
	}

	// Compute the max Y across all points in the low latency lines
	lowMaxSeconds := 0.0
	for _, ld := range lowLines {
		for _, v := range ld.values {
			if v > lowMaxSeconds {
				lowMaxSeconds = v
			}
		}
	}

	// Low-latency chart
	low := plot.New()
	low.Title.Text = fmt.Sprintf("HTTP Request Duration – Low Latency (0–%.1f s)", lowMaxSeconds)
	low.X.Label.Text = "Percentile"
	low.Y.Label.Text = "Time (seconds)"
	low.Y.Min = 0
	// Small headroom above the actual maximum so the top point is not flush with the axis
	low.Y.Max = lowMaxSeconds * 1.1

	// Force int formatting on y-axis tick labels for consistency across charts
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

	for _, ld := range lines {
		pts := make(plotter.XYs, 6)
		for i, v := range ld.values {
			pts[i].X = xPos[i]
			pts[i].Y = v
		}
		line, _ := plotter.NewLine(pts)
		line.Color = ld.color
		line.Width = vg.Points(3)

		full.Add(line)
		full.Legend.Add(ld.name, line)
	}

	// Add only the low-latency lines to the zoomed chart
	for _, ld := range lowLines {
		pts := make(plotter.XYs, 6)
		for i, v := range ld.values {
			pts[i].X = xPos[i]
			pts[i].Y = v
		}
		line, _ := plotter.NewLine(pts)
		line.Color = ld.color
		line.Width = vg.Points(3)

		low.Add(line)
		low.Legend.Add(ld.name, line)
	}

	ticks := []plot.Tick{
		{Value: 1, Label: "min"},
		{Value: 2, Label: "med"},
		{Value: 3, Label: "avg"},
		{Value: 4, Label: "p90"},
		{Value: 5, Label: "p95"},
		{Value: 6, Label: "max"},
	}

	// full chart
	full.Legend.Top = true
	full.X.Tick.Marker = plot.ConstantTicks(ticks)
	full.Save(10*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_duration_breakdown.png"))

	// low chart
	low.Legend.Top = true
	low.X.Tick.Marker = plot.ConstantTicks(ticks)
	low.Save(10*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_duration_low.png"))
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
	bar.Color = color.RGBA{54, 162, 235, 255} // blue

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
	p.Title.Text = fmt.Sprintf("Data Throughput (KB/s) – %.2f iterations/sec", iterRate)
	p.Y.Label.Text = "KB/s"

	values := plotter.Values{
		r.DataReceived.Values.Rate / 1024, // KB/s
		r.DataSent.Values.Rate / 1024,     // KB/s
	}
	// bar width
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = color.RGBA{51, 153, 255, 255} // blue
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

// Data volume chart shows the total bytes received & sent
func makeDataVolumeChart(r Runner, prefix, outDir string) {
	p := plot.New()
	p.Title.Text = "Data Volume (total)"
	p.Y.Label.Text = "MB"

	const bytesPerMB = 1024 * 1024
	values := plotter.Values{
		r.DataReceived.Values.Count / bytesPerMB,
		r.DataSent.Values.Count / bytesPerMB,
	}
	// bar width
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = color.RGBA{51, 153, 255, 255} // blue

	p.Add(bar)

	p.X.Min = -0.5
	p.X.Max = 1.5

	// labels under each bar
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "Received (MB)"},
		{Value: 1, Label: "Sent (MB)"},
	})

	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_data_volume.png"))
}

func vals(m Metric) []float64 {
	return []float64{m.Values.Min, m.Values.Med, m.Values.Avg, m.Values.P90, m.Values.P95, m.Values.Max}
}

// aggregateRunners builds a runner whose metrics are the per-field
// averages across all runs for a test when multiple iterations are ran
func aggregateRunners(rs []Runner) Runner {
	if len(rs) == 0 {
		return Runner{}
	}
	if len(rs) == 1 {
		return rs[0]
	}

	lenRunners := float64(len(rs))
	avgMetric := func(get func(Runner) Metric) Metric {
		var sum Values
		for _, r := range rs {
			v := get(r).Values
			sum.Min += v.Min
			sum.Med += v.Med
			sum.Avg += v.Avg
			sum.P90 += v.P90
			sum.P95 += v.P95
			sum.Max += v.Max
			sum.Rate += v.Rate
			sum.Count += v.Count
		}
		return Metric{
			Values: Values{
				Min:   sum.Min / lenRunners,
				Med:   sum.Med / lenRunners,
				Avg:   sum.Avg / lenRunners,
				P90:   sum.P90 / lenRunners,
				P95:   sum.P95 / lenRunners,
				Max:   sum.Max / lenRunners,
				Rate:  sum.Rate / lenRunners,
				Count: sum.Count / lenRunners,
			},
		}
	}

	return Runner{
		HTTPReqDuration:   avgMetric(func(r Runner) Metric { return r.HTTPReqDuration }),
		HTTPReqWaiting:    avgMetric(func(r Runner) Metric { return r.HTTPReqWaiting }),
		HTTPReqConnecting: avgMetric(func(r Runner) Metric { return r.HTTPReqConnecting }),
		HTTPReqTLS:        avgMetric(func(r Runner) Metric { return r.HTTPReqTLS }),
		HTTPReqSending:    avgMetric(func(r Runner) Metric { return r.HTTPReqSending }),
		HTTPReqReceiving:  avgMetric(func(r Runner) Metric { return r.HTTPReqReceiving }),
		HTTPReqBlocked:    avgMetric(func(r Runner) Metric { return r.HTTPReqBlocked }),
		HTTPReqFailed:     avgMetric(func(r Runner) Metric { return r.HTTPReqFailed }),
		IterationDuration: avgMetric(func(r Runner) Metric { return r.IterationDuration }),
		Iterations:        avgMetric(func(r Runner) Metric { return r.Iterations }),
		Checks:            avgMetric(func(r Runner) Metric { return r.Checks }),
		VUsMax:            avgMetric(func(r Runner) Metric { return r.VUsMax }),
		DataReceived:      avgMetric(func(r Runner) Metric { return r.DataReceived }),
		DataSent:          avgMetric(func(r Runner) Metric { return r.DataSent }),
	}
}
