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
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

var debugEnabled bool

// testModes is a static detector mapping "TestName" (function name only) -> "k6" | "fortio"
var testModes map[string]string

func debugf(format string, args ...interface{}) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, "[charts] "+format+"\n", args...)
	}
}

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

// FortioResult is the explicit name for Fortio/generic perf JSON objects.
// Kept as a type alias for backwards-compatibility with existing helpers.
type FortioResult = genericPerfResult

// Generic perf result (non-k6) with percentiles + histogram, used by some tests (e.g., state, service_invocation)
type percentilePoint struct {
	Percentile float64 `json:"Percentile"`
	Value      float64 `json:"Value"`
}
type durationHistogram struct {
	Count int     `json:"Count"`
	Min   float64 `json:"Min"`
	Max   float64 `json:"Max"`
	Sum   float64 `json:"Sum"`
	Avg   float64 `json:"Avg"`
	Data  []struct {
		Start   float64 `json:"Start"`
		End     float64 `json:"End"`
		Percent float64 `json:"Percent"`
		Count   int     `json:"Count"`
	} `json:"Data"`
	Percentiles []percentilePoint `json:"Percentiles,omitempty"`
}
type genericPerfResult struct {
	RunType            string                 `json:"RunType"`
	RequestedQPS       string                 `json:"RequestedQPS"`
	RequestedDuration  string                 `json:"RequestedDuration"`
	ActualQPS          float64                `json:"ActualQPS"`
	ActualDuration     float64                `json:"ActualDuration"`
	NumThreads         int                    `json:"NumThreads"`
	DurationHistogram  durationHistogram      `json:"DurationHistogram"`
	Percentiles        []percentilePoint      `json:"Percentiles"`
	ErrorsDurationHist *durationHistogram     `json:"ErrorsDurationHistogram,omitempty"`
	Dapr               string                 `json:"Dapr,omitempty"`
	URL                string                 `json:"URL,omitempty"`
	RetCodes           map[string]int         `json:"RetCodes,omitempty"`
	Sizes              map[string]interface{} `json:"Sizes,omitempty"`
	HeaderSizes        map[string]interface{} `json:"HeaderSizes,omitempty"`
	ConnectionStats    map[string]interface{} `json:"ConnectionStats,omitempty"`
	IPCountMap         map[string]int         `json:"IPCountMap,omitempty"`
}
type goTestEvent struct {
	Time    string `json:"Time"`
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test,omitempty"`
	Output  string `json:"Output,omitempty"`
}

const (
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

	// Enable verbose logging via CHARTS_DEBUG env var
	debugEnabled = strings.TrimSpace(os.Getenv("CHARTS_DEBUG")) != ""
	if debugEnabled {
		debugf("debug enabled; reading %s", inputPath)
	}

	// Base output directory (e.g. per release channel/branch)
	baseOutputDir := filepath.Join("charts", "master")
	if err = os.MkdirAll(baseOutputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating charts directory %s: %v\n", baseOutputDir, err)
		os.Exit(1)
	}

	// Build static test mode detector once (best-effort)
	// charts.go lives in tests/perf/report; test roots are one level up ("../")
	testModes = buildTestModes("..")
	debugf("test modes: %v", testModes)
	if len(testModes) > 0 && debugEnabled {
		debugf("detected %d test modes from source scan", len(testModes))
	}

	scanner := bufio.NewScanner(f)
	var (
		collecting      bool
		currentTest     string
		currentPkg      string
		collectMode     string // "k6" or "brace"
		braceDepth      int
		skipCurrentJSON bool
		skipByTest      = make(map[string]bool)
		jsonBuilder     strings.Builder
		generated       int
		// Resource usage tracking per running test
		resourceByTest = make(map[string]*ResourceUsage)
		testPkg        = make(map[string]string)
	)

	for scanner.Scan() {
		var ev goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			// bad line in the json — just skip it...
			continue
		}

		switch ev.Action {
		// Marks the start of a subtest. Reset any partial json we may
		// have been collecting for a previous test
		case "run":
			if ev.Test != "" {
				collecting = false
				currentTest = ev.Test
				currentPkg = ev.Package
				testPkg[currentTest] = currentPkg
				jsonBuilder.Reset()
				// Skip certain harness tests entirely (e.g., TestParamsOpts)
				if strings.HasPrefix(currentTest, "TestParamsOpts") {
					skipByTest[currentTest] = true
					debugf("run start: pkg=%s test=%s (skipped harness test)", currentPkg, currentTest)
					continue
				}
				// Ensure API folder exists even if no results JSON is emitted later
				if apiName, transport, ok := classifyAPIAndTransport(currentPkg, currentTest); ok {
					outDir := filepath.Join(baseOutputDir, apiName)
					if transport != "" {
						outDir = filepath.Join(outDir, transport)
					}
					_ = os.MkdirAll(outDir, 0o755)
					debugf("run start: pkg=%s test=%s outDir=%s", currentPkg, currentTest, outDir)
				} else {
					debugf("run start: pkg=%s test=%s (untracked api)", currentPkg, currentTest)
				}
			}

		// Only consider output that belongs to the current running test
		case "output":
			if ev.Test == "" || ev.Test != currentTest {
				continue
			}
			if skipByTest[currentTest] {
				debugf("skipping test %s", currentTest)
				continue
			}

			// Always try to parse resource usage lines, regardless of JSON collection mode
			if usage := parseResourceUsageLine(ev.Output); usage.component != "" {
				ru := resourceByTest[currentTest]
				if ru == nil {
					ru = &ResourceUsage{}
					resourceByTest[currentTest] = ru
				}
				if usage.component == "app" {
					ru.AppCPUm = usage.cpuMilli
					ru.AppMemMB = usage.memMB
				} else if usage.component == "sidecar" {
					ru.SidecarCPUm = usage.cpuMilli
					ru.SidecarMemMB = usage.memMB
				}
				if !ru.Drawn && ru.AppCPUm > 0 && ru.SidecarCPUm > 0 {
					// Draw charts once we have both app and sidecar
					apiName, transport, ok := classifyAPIAndTransport(testPkg[currentTest], currentTest)
					if ok {

						// Route pubsub bulk by test name keyword as well
						if strings.Contains(ev.Package, "pubsub") && strings.Contains(ev.Package, "bulk") {
							apiName = "pubsub/bulk"
						}
						outDir := filepath.Join(baseOutputDir, apiName)
						if transport != "" {
							outDir = filepath.Join(outDir, transport)
						}
						_ = os.MkdirAll(outDir, 0o755)
						filePrefix := sanitizeName(currentTest)
						makeResourceCPUChart(*ru, filePrefix, outDir)
						makeResourceMemChart(*ru, filePrefix, outDir)
						ru.Drawn = true
					}
				}
			} else if rs, ok := parseRestartLine(ev.Output); ok {
				ru := resourceByTest[currentTest]
				if ru == nil {
					ru = &ResourceUsage{}
					resourceByTest[currentTest] = ru
				}
				if rs.kind == "target" {
					ru.TargetRestarts = rs.count
				} else if rs.kind == "tester" {
					ru.TesterRestarts = rs.count
				}
			}

			// If we are already collecting JSON for a test, keep appending
			// chunks until we see the terminating backtick `
			if collecting {
				if collectMode == "k6" {
					if idx := strings.Index(ev.Output, "`"); idx != -1 {
						// Final chunk = everything before the backtick.
						chunk := strings.TrimSpace(ev.Output[:idx])
						if chunk != "" {
							jsonBuilder.WriteString("\n")
							jsonBuilder.WriteString(chunk)
						}

						k6JSON := jsonBuilder.String()
						parseK6JSONOutput(k6JSON, currentTest, currentPkg, baseOutputDir)
						generated++

						collecting = false
						collectMode = ""
						jsonBuilder.Reset()
						continue
					}
					// Middle chunk for k6: append whole line.
					chunk := strings.TrimSpace(ev.Output)
					if chunk != "" {
						jsonBuilder.WriteString("\n")
						jsonBuilder.WriteString(chunk)
					}
					continue
				} else if collectMode == "brace" {
					// Append line and update brace depth until balanced
					line := ev.Output
					jsonBuilder.WriteString("\n")
					jsonBuilder.WriteString(line)
					braceDepth += strings.Count(line, "{")
					braceDepth -= strings.Count(line, "}")
					if braceDepth <= 0 {
						objJSON := strings.TrimSpace(jsonBuilder.String())
						if skipCurrentJSON {
							debugf("skipped warmup JSON: pkg=%s test=%s", currentPkg, currentTest)
						} else {
							parseFortioJSONOutput(objJSON, currentTest, currentPkg, baseOutputDir)
							generated++
						}
						collecting = false
						collectMode = ""
						skipCurrentJSON = false
						braceDepth = 0
						jsonBuilder.Reset()
					}
					continue
				}
				// Unknown mode: reset
				collecting = false
				collectMode = ""
				jsonBuilder.Reset()
				continue
			}

			// Not collecting atm, so look for the marker that starts the JSON we care about
			// Determine expected mode by base test function name
			baseName := currentTest
			if idx := strings.Index(baseName, "/"); idx != -1 {
				baseName = baseName[:idx]
			}
			if i := strings.Index(baseName, ":_"); i != -1 {
				baseName = baseName[:i]
			}
			expectedMode := testModes[baseName]
			markerIdx := strings.Index(ev.Output, k6SummaryMarker)
			// If no expected mode yet, infer from current output (do not persist)
			if expectedMode == "" {
				if markerIdx != -1 {
					expectedMode = "k6"
				} else {
					lp := strings.ToLower(ev.Output)
					idxResults := strings.Index(lp, "results:")
					idxBrace := strings.Index(ev.Output, "{")
					if idxResults != -1 && idxBrace != -1 && idxResults < idxBrace {
						expectedMode = "fortio"
					}
				}
			}

			// Handle by mode
			if expectedMode == "k6" {
				// Only handle k6 summaries for this test
				// markerIdx is the position of the k6 start-of-summary marker in the current output line.

				if markerIdx == -1 {
					continue
				}
			} else if expectedMode == "fortio" {
				// Only handle Fortio generic JSON blocks for this test
				lp := strings.ToLower(ev.Output)
				idxResults := strings.Index(lp, "results:")
				idxBrace := strings.Index(ev.Output, "{")
				if idxResults != -1 && idxBrace != -1 && idxResults < idxBrace {
					collecting = true
					collectMode = "brace"
					debugf("begin collect (brace): pkg=%s test=%s", currentPkg, currentTest)
					// Detect warmup blocks and mark to skip charting
					prefix := ev.Output[:idxBrace]
					lowerPrefix := strings.ToLower(prefix)
					if strings.Contains(lowerPrefix, "warmup") {
						skipCurrentJSON = true
						debugf("detected warmup block; will skip charts: pkg=%s test=%s", currentPkg, currentTest)
					} else {
						skipCurrentJSON = false
					}
					jsonBuilder.Reset()
					firstChunk := strings.TrimSpace(ev.Output[idxBrace:])
					if firstChunk != "" {
						jsonBuilder.WriteString(firstChunk)
						braceDepth = strings.Count(firstChunk, "{") - strings.Count(firstChunk, "}")
					} else {
						braceDepth = 1
					}
					continue
				}
				continue
			} else {
				// Unknown stays unhandled; wait until we can infer on a later line
				continue
			}

			afterMarker := ev.Output[markerIdx+len(k6SummaryMarker):]

			// Start of a multi-line json block. Save first chunk (which begins
			// with "{") and continue collecting on subsequent lines.
			// ex: {\n"} before "iterations"
			collecting = true
			collectMode = "k6"
			debugf("begin collect (k6): pkg=%s test=%s", currentPkg, currentTest)
			jsonBuilder.Reset()
			firstChunk := strings.TrimSpace(afterMarker)
			if firstChunk != "" {
				jsonBuilder.WriteString(firstChunk)
			}

		// If this is the package-level completion event (no Test field), then
		// we know we've reached the end of all API specific perf tests
		// ex: {"Time":"...","Action":"pass","Package":"github.com/dapr/dapr/tests/perf/workflows","Elapsed":...}
		case "pass", "fail", "skip":
			if ev.Test == "" {
				if collecting {
					fmt.Fprintf(os.Stderr, "Package %s completed while still collecting JSON for test %s\n", ev.Package, currentTest)
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

	fmt.Printf("Generated charts for performance tests in %s\n", baseOutputDir)
}

// classifyAPIAndTransport returns (api, transport, ok) derived from the package path.
// Known apis: "actors", "service_invocation", "workflows", "state", "pubsub", "pubsub/bulk", "configuration".
// For any other tests under tests/perf/..., we fallback to using the package's base name as the api folder.
// transport is "" or "http" or "grpc" when applicable.
func classifyAPIAndTransport(pkg, test string) (string, string, bool) {
	pkgLower := strings.ToLower(pkg)
	testLower := strings.ToLower(test)
	seg := strings.ToLower(filepath.Base(pkg))

	// Workflows
	if strings.Contains(pkgLower, "/workflows") || strings.Contains(pkgLower, "/workflow") ||
		strings.Contains(seg, "workflow") || seg == "workflows" {
		return "workflows", "", true
	}

	// Service invocation
	if strings.Contains(pkgLower, "/service_invocation") || strings.HasPrefix(seg, "service_invocation") {
		transport := ""
		if strings.Contains(pkgLower, "grpc") || strings.Contains(testLower, "grpc") || strings.Contains(seg, "grpc") {
			transport = "grpc"
		} else if strings.Contains(pkgLower, "http") || strings.Contains(testLower, "http") || strings.Contains(seg, "http") {
			transport = "http"
		}
		return "service_invocation", transport, true
	}

	// Actors: many packages begin with "actor_..."
	if strings.Contains(pkgLower, "/actor") || strings.Contains(pkgLower, "/actors") ||
		strings.HasPrefix(seg, "actor") || strings.Contains(seg, "actors") {
		transport := ""
		if strings.Contains(pkgLower, "grpc") || strings.Contains(testLower, "grpc") || strings.Contains(seg, "grpc") {
			transport = "grpc"
		} else if strings.Contains(pkgLower, "http") || strings.Contains(testLower, "http") || strings.Contains(seg, "http") {
			transport = "http"
		}
		return "actors", transport, true
	}

	// State only has grpc so leave in state dir and dont split by transport
	if strings.Contains(pkgLower, "/state") || strings.HasPrefix(seg, "state") {
		// For state, do not split into transport subfolders; keep a single 'state' folder
		return "state", "", true
	}

	// PubSub
	if strings.Contains(pkgLower, "/pubsub") || strings.HasPrefix(seg, "pubsub") {
		transport := ""
		if strings.Contains(pkgLower, "grpc") || strings.Contains(testLower, "grpc") || strings.Contains(seg, "grpc") {
			transport = "grpc"
		} else if strings.Contains(pkgLower, "http") || strings.Contains(testLower, "http") || strings.Contains(seg, "http") {
			transport = "http"
		}
		// Bulk pubsub (split into subfolder)
		if strings.Contains(pkgLower, "/pubsub_bulk") || strings.HasPrefix(seg, "pubsub_bulk") ||
			strings.Contains(pkgLower, "bulk_publish") || strings.Contains(seg, "bulk") {
			return "pubsub/bulk", transport, true
		}
		return "pubsub", transport, true
	}

	// Configuration
	if strings.Contains(pkgLower, "/configuration") || seg == "configuration" ||
		strings.HasPrefix(seg, "configuration_") {
		transport := ""
		if strings.Contains(pkgLower, "grpc") || strings.Contains(testLower, "grpc") || strings.Contains(seg, "grpc") {
			transport = "grpc"
		} else if strings.Contains(pkgLower, "http") || strings.Contains(testLower, "http") || strings.Contains(seg, "http") {
			transport = "http"
		}
		return "configuration", transport, true
	}

	// Fallback: chart any other tests under tests/perf/* using the package base name
	if strings.Contains(pkgLower, "/tests/perf") {
		api := seg
		transport := ""
		if strings.Contains(pkgLower, "grpc") || strings.Contains(testLower, "grpc") || strings.Contains(seg, "grpc") {
			transport = "grpc"
		} else if strings.Contains(pkgLower, "http") || strings.Contains(testLower, "http") || strings.Contains(seg, "http") {
			transport = "http"
		}
		// Avoid empty api names
		//if api == "" || api == "perf" {
		//	api = "misc"
		//}
		return api, transport, true
	}

	return "", "", false
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

// stripPubsubParamSuffix removes parameter suffixes from pubsub test names so
// different parameterized runs aggregate under the same base test.
// Example: "TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk"
// -> "TestPubsubBulkPublishHttpPerformance"
func stripPubsubParamSuffix(name string) string {
	// Heuristic: keep everything up to the first '_' if the prefix looks like a pubsub test
	if strings.HasPrefix(name, "TestPubsub") || strings.HasPrefix(name, "TestBulkPubsub") {
		if idx := strings.Index(name, "_"); idx > 0 {
			return name[:idx]
		}
	}
	return name
}

// Perf test summary for x iterations. Show success/failure/VUs.
func processSummary(k6JSON, testName, pkg, baseOutputDir string) {
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
	// Note: filePrefix unused in aggregate processing; per-run charts are generated elsewhere.

	// Determine API and transport directory and ensure it exists (limit to actors, service_invocation, workflows)
	apiName, transport, ok := classifyAPIAndTransport(pkg, testName)
	if !ok {
		// Not a target API (for now)
		debugf("k6 summary skipped (untracked api): pkg=%s test=%s", pkg, testName)
		return
	}
	// If pubsub API and test name indicates "bulk", place under pubsub/bulk
	if strings.HasPrefix(apiName, "pubsub") && strings.Contains(strings.ToLower(testName), "bulk") {
		apiName = "pubsub/bulk"
	}
	outDir := filepath.Join(baseOutputDir, apiName)
	if transport != "" {
		outDir = filepath.Join(outDir, transport)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating charts directory %s: %v\n", outDir, err)
		return
	}
	debugf("k6 summary parsed: pkg=%s test=%s api=%s transport=%s outDir=%s", pkg, testName, apiName, transport, outDir)

	// Keep results separated per API/package
	mapKey := apiName + "/"
	if transport != "" {
		mapKey += transport + "/"
	}
	// For pubsub, aggregate across parameterized variants by base test name
	if strings.HasPrefix(apiName, "pubsub") {
		base := stripPubsubParamSuffix(key)
		mapKey += base
		key = base
	} else {
		mapKey += key
	}

	result, exists := combinedResults[mapKey]
	if !exists {
		result = combinedTestResult{
			name:    key,
			outDir:  outDir,
			runners: []Runner{},
		}
	}
	result.runners = append(result.runners, r)
	combinedResults[mapKey] = result
}

// parseK6JSONOutput parses a k6 summary JSON and feeds charts/aggregation.
func parseK6JSONOutput(k6JSON, testName, pkg, baseOutputDir string) {
	processSummary(k6JSON, testName, pkg, baseOutputDir)
}

// processGenericSummary parses non-k6 JSON perf outputs and converts them into a Runner,
// then stores them using the same aggregation mechanism.
func processGenericSummary(objJSON, testName, pkg, baseOutputDir string) {
	objJSON = strings.TrimSpace(objJSON)
	if objJSON == "" || !strings.HasPrefix(objJSON, "{") {
		return
	}
	// Sanitize known problematic fields and formatting in JSON emitted by tests
	objJSON = sanitizePerfJSON(objJSON)
	// Attempt lightweight repair for truncated JSON blocks
	objJSON = repairJSONClosers(objJSON)
	// Validate before attempting to unmarshal
	if !json.Valid([]byte(objJSON)) {
		debugf("invalid JSON after sanitize/repair: pkg=%s test=%s", pkg, testName)
		return
	}

	var res genericPerfResult
	if err := json.Unmarshal([]byte(objJSON), &res); err != nil {
		// Not a recognized generic perf result; ignore
		debugf("generic parse failed: pkg=%s test=%s err=%v", pkg, testName, err)
		return
	}

	// Minimal validation
	if len(res.Percentiles) == 0 && res.DurationHistogram.Count == 0 {
		debugf("generic parse skipped (no data): pkg=%s test=%s where res=%s", pkg, testName, res)
		return
	}

	// Convert into Runner
	r := convertGenericPerfToRunner(res)

	// Group multiple runs of the same test by stripping the subtest suffixes.
	baseName := testName
	if i := strings.Index(baseName, ":_"); i != -1 {
		baseName = baseName[:i]
	}
	key := sanitizeName(baseName)
	filePrefix := sanitizeName(testName) // preserve subtest/run suffix for per-run charts

	apiName, transport, ok := classifyAPIAndTransport(pkg, testName)
	if !ok {
		// Not one of the target APIs for now
		debugf("generic summary skipped (untracked api): pkg=%s test=%s", pkg, testName)
		return
	}
	// If pubsub API and test name indicates "bulk", place under pubsub/bulk
	if strings.HasPrefix(apiName, "pubsub") && strings.Contains(strings.ToLower(testName), "bulk") && !strings.HasPrefix(apiName, "pubsub/bulk") {
		apiName = "pubsub/bulk"
	}
	outDir := filepath.Join(baseOutputDir, apiName)
	if transport != "" {
		outDir = filepath.Join(outDir, transport)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating charts directory %s: %v\n", outDir, err)
		return
	}
	debugf("generic summary parsed: pkg=%s test=%s api=%s transport=%s outDir=%s runType=%s reqQPS=%s actualQPS=%.2f",
		pkg, testName, apiName, transport, outDir, res.RunType, res.RequestedQPS, res.ActualQPS)

	// Per-run charts specific to generic perf outputs
	makeQPSChart(res, filePrefix, outDir)
	makeDurationRequestedVsActualChart(res, filePrefix, outDir)
	makeDurationHistogramCharts(res, filePrefix, outDir)
	if strings.TrimSpace(os.Getenv("CHARTS_RETCODES")) != "" {
		makeRetCodesChart(res, filePrefix, outDir)
	}
	makeConnectionStatsChart(res, filePrefix, outDir)
	makeSizesCharts(res, filePrefix, outDir)

	mapKey := apiName + "/"
	if transport != "" {
		mapKey += transport + "/"
	}
	// For pubsub, aggregate across parameterized variants by base test name
	if strings.HasPrefix(apiName, "pubsub") {
		base := stripPubsubParamSuffix(key)
		mapKey += base
		key = base
	} else {
		mapKey += key
	}

	result, exists := combinedResults[mapKey]
	if !exists {
		result = combinedTestResult{
			name:    key,
			outDir:  outDir,
			runners: []Runner{},
		}
	}
	result.runners = append(result.runners, r)
	combinedResults[mapKey] = result
}

// parseFortioJSONOutput parses a Fortio-style JSON block and feeds charts/aggregation.
func parseFortioJSONOutput(objJSON, testName, pkg, baseOutputDir string) {
	processGenericSummary(objJSON, testName, pkg, baseOutputDir)
}

// convertGenericPerfToRunner maps a generic perf result (percentiles + histogram) into the Runner format.
func convertGenericPerfToRunner(res genericPerfResult) Runner {
	// Helper to get a percentile value
	getPct := func(p float64) (float64, bool) {
		// Prefer top-level Percentiles if present
		if len(res.Percentiles) > 0 {
			for _, pt := range res.Percentiles {
				if int(pt.Percentile*10) == int(p*10) || pt.Percentile == p {
					return pt.Value, true
				}
			}
		}
		// Fallback to DurationHistogram.Percentiles
		for _, pt := range res.DurationHistogram.Percentiles {
			if int(pt.Percentile*10) == int(p*10) || pt.Percentile == p {
				return pt.Value, true
			}
		}
		return 0, false
	}
	// Derive a success rate from RetCodes when present; default to 1.0 (100%) if unknown
	computeSuccessRate := func() float64 {
		if len(res.RetCodes) == 0 {
			return 1.0
		}
		total := 0
		success := 0
		for k, v := range res.RetCodes {
			total += v
			lk := strings.ToLower(strings.TrimSpace(k))
			if lk == "200" || lk == "2xx" || lk == "ok" || lk == "serving" {
				success += v
			}
		}
		if total == 0 {
			return 1.0
		}
		return float64(success) / float64(total)
	}
	med, _ := getPct(50)
	p90, _ := getPct(90)
	p95, ok95 := getPct(95)
	if !ok95 {
		// Fallback to 99 or 99.9 if 95 is not present
		if v, ok := getPct(95.0); ok {
			p95 = v
		} else if v, ok := getPct(99.0); ok {
			p95 = v
		} else if v, ok := getPct(99.9); ok {
			p95 = v
		}
	}
	min := res.DurationHistogram.Min
	max := res.DurationHistogram.Max
	avg := res.DurationHistogram.Avg
	count := float64(res.DurationHistogram.Count)
	rate := res.ActualQPS
	checksRate := computeSuccessRate()

	vals := func() Values {
		return Values{
			Min:   min,
			Med:   med,
			Avg:   avg,
			P90:   p90,
			P95:   p95,
			Max:   max,
			Rate:  rate,
			Count: count,
		}
	}

	return Runner{
		HTTPReqDuration:   Metric{Values: vals()},
		HTTPReqWaiting:    Metric{Values: Values{}},
		HTTPReqConnecting: Metric{Values: Values{}},
		HTTPReqTLS:        Metric{Values: Values{}},
		HTTPReqSending:    Metric{Values: Values{}},
		HTTPReqReceiving:  Metric{Values: Values{}},
		HTTPReqBlocked:    Metric{Values: Values{}},
		HTTPReqFailed:     Metric{Values: Values{}},
		IterationDuration: Metric{Values: Values{}},
		Iterations:        Metric{Values: Values{Count: count, Rate: rate}},
		Checks:            Metric{Values: Values{Rate: checksRate}},
		VUsMax:            Metric{Values: Values{}},
		DataReceived:      Metric{Values: Values{}},
		DataSent:          Metric{Values: Values{}},
	}
}

// Helpers and charts for generic perf outputs
func parseRequestedQPS(qps string) float64 {
	if qps == "" {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(qps), 64)
	if err != nil {
		return 0
	}
	return v
}

func parseRequestedDurationSeconds(dur string) float64 {
	if dur == "" {
		return 0
	}
	td, err := time.ParseDuration(dur)
	if err != nil {
		return 0
	}
	return td.Seconds()
}

func normalizeActualDurationSeconds(actual float64) float64 {
	// Heuristic: values around 6e10 are likely nanoseconds (~60s)
	if actual > 1e6 {
		return actual / 1e9
	}
	return actual
}

// QPS requested vs actual
func makeQPSChart(res genericPerfResult, prefix, outDir string) {
	req := parseRequestedQPS(res.RequestedQPS)
	act := res.ActualQPS

	p := plot.New()
	p.Title.Text = fmt.Sprintf("QPS – Requested vs Actual (threads=%d)", res.NumThreads)
	p.Y.Label.Text = "QPS"

	values := plotter.Values{req, act}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = color.RGBA{54, 162, 235, 255}
	p.Add(bar)

	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "Requested"},
		{Value: 1, Label: "Actual"},
	})

	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_qps.png"))
}

// Duration requested vs actual
func makeDurationRequestedVsActualChart(res genericPerfResult, prefix, outDir string) {
	req := parseRequestedDurationSeconds(res.RequestedDuration)
	act := normalizeActualDurationSeconds(res.ActualDuration)

	p := plot.New()
	p.Title.Text = fmt.Sprintf("Duration – Requested vs Actual (s) (threads=%d)", res.NumThreads)
	p.Y.Label.Text = "Seconds"

	values := plotter.Values{req, act}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = color.RGBA{255, 159, 64, 255}
	p.Add(bar)

	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "Requested (s)"},
		{Value: 1, Label: "Actual (s)"},
	})

	p.Save(7*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_duration_requested_vs_actual.png"))
}

// Histogram charts: percent per bin and count per bin
func makeDurationHistogramCharts(res genericPerfResult, prefix, outDir string) {
	// Build labels and values, skipping zero-count bins and keeping labels readable (ms)
	raw := res.DurationHistogram.Data
	if len(raw) == 0 {
		return
	}
	labels := make([]plot.Tick, 0, len(raw))
	percValues := make(plotter.Values, 0, len(raw))
	countValues := make(plotter.Values, 0, len(raw))
	for _, b := range raw {
		if b.Count == 0 {
			continue
		}
		startMs := b.Start * 1000.0
		endMs := b.End * 1000.0
		startInt := int(math.Round(startMs))
		endInt := int(math.Round(endMs))
		idx := float64(len(percValues))
		labels = append(labels, plot.Tick{
			Value: idx,
			Label: fmt.Sprintf("[%d–%d]", startInt, endInt),
		})
		percValues = append(percValues, b.Percent)
		countValues = append(countValues, float64(b.Count))
	}
	if len(percValues) == 0 {
		return
	}
	// Reduce label density if too many bins
	if len(labels) > 25 {
		step := (len(labels) + 24) / 25 // ceil(n/25)
		slim := make([]plot.Tick, 0, (len(labels)+step-1)/step)
		for i := 0; i < len(labels); i += step {
			slim = append(slim, labels[i])
		}
		labels = slim
	}

	// Percent chart
	p1 := plot.New()
	p1.Title.Text = "Latency Histogram – Percent per Bin"
	p1.X.Label.Text = "Latency bucket (ms)"
	p1.Y.Label.Text = "Percent (%)"
	bar1, _ := plotter.NewBarChart(percValues, vg.Points(20))
	bar1.Color = color.RGBA{75, 192, 192, 255}
	p1.Add(bar1)
	p1.X.Min = -0.5
	p1.X.Max = float64(len(percValues)) - 0.5
	p1.X.Tick.Marker = plot.ConstantTicks(labels)
	p1.Save(14*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_histogram_percent.png"))

	// Count chart
	p2 := plot.New()
	p2.Title.Text = "Latency Histogram – Count per Bin"
	p2.X.Label.Text = "Latency bucket (ms)"
	p2.Y.Label.Text = "Count"
	bar2, _ := plotter.NewBarChart(countValues, vg.Points(20))
	bar2.Color = color.RGBA{153, 102, 255, 255}
	p2.Add(bar2)
	p2.X.Min = -0.5
	p2.X.Max = float64(len(countValues)) - 0.5
	p2.X.Tick.Marker = plot.ConstantTicks(labels)
	p2.Save(14*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_histogram_count.png"))
}

// Resource usage capture and charts
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
	component string  // "app" or "sidecar"
	cpuMilli  float64 // mCPU
	memMB     float64
}

func parseResourceUsageLine(line string) parsedUsage {
	l := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(l, "consumed") || !strings.Contains(l, "cpu") || !strings.Contains(l, "memory") {
		return parsedUsage{}
	}
	component := ""
	if strings.Contains(l, "target dapr app consumed") {
		component = "app"
	} else if strings.Contains(l, "target dapr sidecar consumed") {
		component = "sidecar"
	} else {
		return parsedUsage{}
	}
	// Extract CPU "605m" between "consumed " and " cpu"
	cpuMilli := 0.0
	if idx := strings.Index(l, "consumed "); idx != -1 {
		rest := l[idx+len("consumed "):]
		if j := strings.Index(rest, " cpu"); j != -1 {
			cpuStr := strings.TrimSpace(rest[:j])
			if strings.HasSuffix(cpuStr, "m") {
				cpuStr = strings.TrimSuffix(cpuStr, "m")
				if v, err := strconv.ParseFloat(cpuStr, 64); err == nil {
					cpuMilli = v
				}
			} else {
				// If value not suffixed with m, treat as cores -> convert to mCPU
				if v, err := strconv.ParseFloat(cpuStr, 64); err == nil {
					cpuMilli = v * 1000.0
				}
			}
		}
	}
	// Extract Memory between " and " and "mb of memory"
	memMB := 0.0
	if k := strings.Index(l, " and "); k != -1 {
		rest := l[k+len(" and "):]
		if m := strings.Index(rest, "mb of memory"); m != -1 {
			memStr := strings.TrimSpace(rest[:m])
			if v, err := strconv.ParseFloat(memStr, 64); err == nil {
				memMB = v
			}
		}
	}
	return parsedUsage{component: component, cpuMilli: cpuMilli, memMB: memMB}
}

type parsedRestarts struct {
	kind  string // "target" or "tester"
	count int
}

func parseRestartLine(line string) (parsedRestarts, bool) {
	l := strings.ToLower(strings.TrimSpace(line))
	if !strings.Contains(l, "restarted") || !strings.Contains(l, "times") {
		return parsedRestarts{}, false
	}
	kind := ""
	if strings.Contains(l, "target dapr app or sidecar restarted") {
		kind = "target"
	} else if strings.Contains(l, "tester app or sidecar restarted") {
		kind = "tester"
	} else {
		return parsedRestarts{}, false
	}
	// Extract integer between "restarted " and " times"
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
	return parsedRestarts{kind: kind, count: count}, true
}

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
	bar.Color = color.RGBA{255, 205, 86, 255} // yellow
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
	bar.Color = color.RGBA{255, 99, 132, 255} // pinkish-red
	p.Add(bar)
	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "App"},
		{Value: 1, Label: "Sidecar"},
	})
	p.Save(6*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_resource_memory.png"))
}

// RetCodes distribution chart (e.g., HTTP codes or SERVING count)
func makeRetCodesChart(res genericPerfResult, prefix, outDir string) {
	if len(res.RetCodes) == 0 {
		return
	}
	// Collect keys deterministically
	type kv struct {
		key   string
		value int
	}
	kvs := make([]kv, 0, len(res.RetCodes))
	for k, v := range res.RetCodes {
		kvs = append(kvs, kv{key: k, value: v})
	}
	// Simple stable order: lexical
	for i := 0; i < len(kvs)-1; i++ {
		for j := i + 1; j < len(kvs); j++ {
			if kvs[j].key < kvs[i].key {
				kvs[i], kvs[j] = kvs[j], kvs[i]
			}
		}
	}
	labels := make([]plot.Tick, len(kvs))
	values := make(plotter.Values, len(kvs))
	for i, p := range kvs {
		labels[i] = plot.Tick{Value: float64(i), Label: p.key}
		values[i] = float64(p.value)
	}
	p := plot.New()
	p.Title.Text = "Return Codes"
	p.Y.Label.Text = "Count"
	bar, _ := plotter.NewBarChart(values, vg.Points(25))
	bar.Color = color.RGBA{51, 153, 255, 255}
	p.Add(bar)
	p.X.Min = -0.5
	p.X.Max = float64(len(kvs)) - 0.5
	p.X.Tick.Marker = plot.ConstantTicks(labels)
	p.Save(10*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_retcodes.png"))
}

// ConnectionStats min/avg/max chart if available
func makeConnectionStatsChart(res genericPerfResult, prefix, outDir string) {
	if len(res.ConnectionStats) == 0 {
		return
	}
	// Case-insensitive numeric lookup helper supporting interface{} values
	num := func(v interface{}) (float64, bool) {
		switch t := v.(type) {
		case float64:
			return t, true
		case int:
			return float64(t), true
		case int64:
			return float64(t), true
		case json.Number:
			if f, err := t.Float64(); err == nil {
				return f, true
			}
		}
		return 0, false
	}
	get := func(name string) (float64, bool) {
		name = strings.ToLower(name)
		for k, v := range res.ConnectionStats {
			if strings.ToLower(k) != name {
				continue
			}
			return num(v)
		}
		return 0, false
	}
	min, okMin := get("min")
	avg, okAvg := get("avg")
	max, okMax := get("max")
	stddev, okStd := get("stddev")
	count, _ := get("count")
	if !okMin && !okAvg && !okMax && !okStd {
		return
	}
	p := plot.New()
	if count > 0 {
		p.Title.Text = fmt.Sprintf("Connection Stats (seconds) – Count: %.0f", count)
	} else {
		p.Title.Text = "Connection Stats (seconds)"
	}
	// Decide Y-axis unit and scaling based on the magnitude of values
	unit := "Seconds"
	scale := 1.0
	// Build values and labels dynamically to include StdDev when present (omit Sum to avoid dwarfing).
	type labelValue struct {
		label string
		val   float64
	}
	lvs := make([]labelValue, 0, 5)
	if okMin {
		lvs = append(lvs, labelValue{"Min", min})
	}
	if okAvg {
		lvs = append(lvs, labelValue{"Avg", avg})
	}
	if okMax {
		lvs = append(lvs, labelValue{"Max", max})
	}
	if okStd {
		lvs = append(lvs, labelValue{"StdDev", stddev})
	}
	// Determine max among values to pick unit
	maxVal := 0.0
	for _, lv := range lvs {
		if lv.val > maxVal {
			maxVal = lv.val
		}
	}
	if maxVal < 0.0005 { // < 0.5 ms -> show microseconds
		unit = "Microseconds"
		scale = 1e6
	} else if maxVal < 0.01 { // < 10 ms -> show milliseconds
		unit = "Milliseconds"
		scale = 1e3
	}
	p.Y.Label.Text = unit
	values := make(plotter.Values, len(lvs))
	ticks := make([]plot.Tick, len(lvs))
	for i, lv := range lvs {
		values[i] = lv.val * scale
		ticks[i] = plot.Tick{Value: float64(i), Label: lv.label}
	}
	bar, _ := plotter.NewBarChart(values, vg.Points(35))
	bar.Color = color.RGBA{102, 194, 165, 255}
	p.Add(bar)
	p.X.Min = -0.5
	p.X.Max = float64(len(values)) - 0.5
	p.X.Tick.Marker = plot.ConstantTicks(ticks)
	p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_connection_stats.png"))
}

// Sizes and HeaderSizes charts (min/avg/max)
func makeSizesCharts(res genericPerfResult, prefix, outDir string) {
	type stats struct {
		min, avg, max float64
		stddev        float64
		count         float64
		ok            bool
	}
	// Extracts min/avg/max from either:
	// - map[string]float64 with keys Min/Avg/Max
	// - map[string]interface{} with keys Min/Avg/Max (numbers), possibly with extra fields
	extract := func(m map[string]interface{}) stats {
		if m == nil || len(m) == 0 {
			return stats{}
		}
		lowerNum := func(k string) (float64, bool) {
			k = strings.ToLower(k)
			for kk, vv := range m {
				if strings.ToLower(kk) != k {
					continue
				}
				switch t := vv.(type) {
				case float64:
					return t, true
				case int:
					return float64(t), true
				case int64:
					return float64(t), true
				case json.Number:
					if f, err := t.Float64(); err == nil {
						return f, true
					}
				}
			}
			return 0, false
		}
		min, okMin := lowerNum("min")
		avg, okAvg := lowerNum("avg")
		max, okMax := lowerNum("max")
		stddev, _ := lowerNum("stddev")
		count, _ := lowerNum("count")
		return stats{min: min, avg: avg, max: max, stddev: stddev, count: count, ok: okMin || okAvg || okMax || stddev != 0}
	}
	if s := extract(res.Sizes); s.ok {
		p := plot.New()
		if s.count > 0 {
			p.Title.Text = fmt.Sprintf("Payload Sizes (bytes) – Count: %.0f", s.count)
		} else {
			p.Title.Text = "Payload Sizes (bytes)"
		}
		p.Y.Label.Text = "Bytes"
		// Show Min/Avg/Max and StdDev when available. Omit Sum to avoid dwarfing other bars.
		v := []float64{s.min, s.avg, s.max}
		labels := []plot.Tick{
			{Value: 0, Label: "Min"},
			{Value: 1, Label: "Avg"},
			{Value: 2, Label: "Max"},
		}
		if s.stddev > 0 {
			labels = append(labels, plot.Tick{Value: float64(len(v)), Label: "StdDev"})
			v = append(v, s.stddev)
		}
		values := plotter.Values(v)
		bar, _ := plotter.NewBarChart(values, vg.Points(35))
		bar.Color = color.RGBA{75, 192, 192, 255}
		p.Add(bar)
		p.X.Min = -0.5
		p.X.Max = float64(len(values)) - 0.5
		p.X.Tick.Marker = plot.ConstantTicks(labels)
		p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_payload_sizes.png"))
	}
	if hs := extract(res.HeaderSizes); hs.ok {
		p := plot.New()
		if hs.count > 0 {
			p.Title.Text = fmt.Sprintf("Header Sizes (bytes) – Count: %.0f", hs.count)
		} else {
			p.Title.Text = "Header Sizes (bytes)"
		}
		p.Y.Label.Text = "Bytes"
		// Show Min/Avg/Max and StdDev when available. Omit Sum to avoid dwarfing bars.
		v := []float64{hs.min, hs.avg, hs.max}
		labels := []plot.Tick{
			{Value: 0, Label: "Min"},
			{Value: 1, Label: "Avg"},
			{Value: 2, Label: "Max"},
		}
		if hs.stddev > 0 {
			labels = append(labels, plot.Tick{Value: float64(len(v)), Label: "StdDev"})
			v = append(v, hs.stddev)
		}
		values := plotter.Values(v)
		bar, _ := plotter.NewBarChart(values, vg.Points(35))
		bar.Color = color.RGBA{255, 159, 64, 255}
		p.Add(bar)
		p.X.Min = -0.5
		p.X.Max = float64(len(values)) - 0.5
		p.X.Tick.Marker = plot.ConstantTicks(labels)
		p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_header_sizes.png"))
	}
}

// sanitizePerfJSON removes problematic fields and line-break artifacts from JSON emitted by perf tests.
// - Drops any "Payload": "..." field and base64-only continuation lines
// - Fixes common trailing comma issues
func sanitizePerfJSON(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	skipPayload := false
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if skipPayload {
			// Skip continuation lines of the Payload field until we detect the closing quote+comma.
			// Many logs split the base64 payload across lines that do NOT start with a quote.
			// Keep skipping until a line contains the terminating '",'
			if strings.Contains(l, "\",") || strings.HasSuffix(l, "\"") {
				// This line ends the payload value; skip it and stop skipping next lines
				skipPayload = false
				continue
			}
			// Still within payload continuation; skip this line
			continue
		}
		if strings.HasPrefix(l, "\"Payload\"") || strings.Contains(l, "\"Payload\":") {
			// Drop the Payload field entirely. If it's a single-line value ending with ", or ",
			// skip just this line; otherwise start skipping continuation lines until closing quote.
			if strings.Contains(l, "\",") || strings.HasSuffix(l, "\"") {
				// single-line payload; skip this line only
				continue
			}
			skipPayload = true
			continue
		}
		out = append(out, line)
	}
	s = strings.Join(out, "\n")
	// remove trailing commas before ] or }
	s = strings.ReplaceAll(s, ",]", "]")
	s = strings.ReplaceAll(s, ",}", "}")
	// truncate any trailing characters after the last closing brace
	if idx := strings.LastIndex(s, "}"); idx != -1 {
		s = s[:idx+1]
	}
	return s
}

// repairJSONClosers adds missing closing braces/brackets if we detect more openings than closings.
// This is a defensive fix for logs where the JSON object may be truncated by line buffering.
func repairJSONClosers(s string) string {
	openCurly := strings.Count(s, "{")
	closeCurly := strings.Count(s, "}")
	openSquare := strings.Count(s, "[")
	closeSquare := strings.Count(s, "]")
	var b strings.Builder
	b.WriteString(s)
	// Close arrays first, then objects, as most blocks are objects containing arrays
	for i := 0; i < openSquare-closeSquare; i++ {
		b.WriteString("]")
	}
	for i := 0; i < openCurly-closeCurly; i++ {
		b.WriteString("}")
	}
	return b.String()
}

// buildTestModes scans the tests/perf source tree to determine which tests use k6 vs Fortio.
// It returns a map keyed by "TestName" (function name only) with values "k6" or "fortio".
func buildTestModes(root string) map[string]string {
	result := map[string]string{}
	skipDir := func(p string, d fs.DirEntry) bool {
		if !d.IsDir() {
			return false
		}
		base := filepath.Base(p)
		if base == "report" || base == "utils" || base == "dist" || base == "charts" || base == "logs" {
			return true
		}
		// Skip any nested asset directories
		if strings.Contains(p, string(filepath.Separator)+"report"+string(filepath.Separator)) ||
			strings.Contains(p, string(filepath.Separator)+"dist"+string(filepath.Separator)) ||
			strings.Contains(p, string(filepath.Separator)+"logs"+string(filepath.Separator)) {
			return true
		}
		return false
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if skipDir(path, d) {
			return fs.SkipDir
		}
		// Consider only per-package test files one level below root
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Dir(path) == filepath.Clean(root) {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(strings.ToLower(base), "readme") ||
			strings.HasPrefix(strings.ToLower(base), "test_params") ||
			strings.HasPrefix(strings.ToLower(base), "test_result") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := string(b)
		// Detect mode for this file
		mode := ""
		if strings.Contains(src, "NewK6(") || strings.Contains(src, ".NewK6(") {
			mode = "k6"
		} else {
			//mode = "fortio"
			hasPerfImport := strings.Contains(src, "\"github.com/dapr/dapr/tests/perf\"") ||
				strings.Contains(src, "\"github.com/dapr/dapr/tests/perf/framework/perf\"") ||
				strings.Contains(src, "\"tests/perf\"") ||
				strings.Contains(src, "\"tests/perf/framework/perf\"")
			callsParams := strings.Contains(src, ".Params(") || strings.Contains(src, "perf.Params(")
			if hasPerfImport && callsParams {
				mode = "fortio"
			} else if strings.Contains(src, "dapr test results: {") || strings.Contains(strings.ToLower(src), "results: {") {
				mode = "fortio"
			}
		}
		if mode == "" {
			debugf("cassie mode empty - Skipping %s test", src)
			return nil
		}

		_ = strings.ToLower(filepath.Base(filepath.Dir(path))) // pkg base no longer used as part of key
		for _, line := range strings.Split(src, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "func Test") && strings.Contains(l, "(t *testing.T)") {
				name := l[len("func "):]
				if idx := strings.Index(name, "("); idx != -1 {
					name = strings.TrimSpace(name[:idx])
				}
				if name != "" {
					// Store by function name only
					result[name] = mode
				}
			}
		}
		return nil
	})
	return result
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
	// Ignore 'max' point to avoid single outliers blowing up the scale.
	lowMaxSeconds := 0.0
	for _, ld := range lowLines {
		for i, v := range ld.values {
			// values order: min, med, avg, p90, p95, max (index 5 is max)
			if i == 5 {
				continue
			}
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

	// helper to check if a series has all zeros
	isAllZero := func(vals []float64) bool {
		for _, v := range vals {
			if v != 0 {
				return false
			}
		}
		return true
	}

	anyPlottedFull := false
	anyPlottedLow := false
	for _, ld := range lines {
		if isAllZero(ld.values) {
			continue
		}
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
		anyPlottedFull = true
	}

	// Add only the low-latency lines to the zoomed chart
	for _, ld := range lowLines {
		if isAllZero(ld.values) {
			continue
		}
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
		anyPlottedLow = true
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
	if anyPlottedFull {
		full.Legend.Top = true
		full.X.Tick.Marker = plot.ConstantTicks(ticks)
		full.Save(10*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_duration_breakdown.png"))
	}

	// low chart
	if anyPlottedLow {
		low.Legend.Top = true
		low.X.Tick.Marker = plot.ConstantTicks(ticks)
		low.Save(10*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_duration_low.png"))
	}
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

	recvKBs := r.DataReceived.Values.Rate / 1024 // KB/s
	sentKBs := r.DataSent.Values.Rate / 1024     // KB/s
	// Skip if both series are zero (no data available)
	if recvKBs == 0 && sentKBs == 0 {
		return
	}
	values := plotter.Values{recvKBs, sentKBs}
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

	const (
		bytesPerKB = 1024.0
		bytesPerMB = 1024.0 * 1024.0
		bytesPerGB = 1024.0 * 1024.0 * 1024.0
	)

	receivedBytes := r.DataReceived.Values.Count
	sentBytes := r.DataSent.Values.Count
	// Skip chart if both values are zero (no data available for this test)
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
	// bar width
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = color.RGBA{51, 153, 255, 255} // blue

	p.Add(bar)

	p.X.Min = -0.5
	p.X.Max = 1.5

	// labels under each bar
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: fmt.Sprintf("Received (%s)", unit)},
		{Value: 1, Label: fmt.Sprintf("Sent (%s)", unit)},
	})

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
