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
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Collect all runs of the same test for aggregates when more than 1 iteration is run
type combinedTestResult struct {
	name    string
	outDir  string
	runners []Runner
}

var combinedResults = make(map[string]combinedTestResult)

var debugEnabled bool

// PerfSuite represents the perf suite type that produced the JSON summary: k6 vs fortio
type PerfSuite int

const (
	SuiteUnknown PerfSuite = iota
	SuiteK6
	SuiteFortio
)

// testModes is a mapping of test func name to perf suite kind: k6/fortio
var testModes map[string]string

// used for local debugging when tweaking and adding charts
func debugf(format string, args ...interface{}) {
	if debugEnabled {
		log.Printf("[charts] "+format+"\n", args...)
	}
}

type Values struct {
	Min   float64 `json:"min"`
	Med   float64 `json:"med"`
	P75   float64 `json:"p(75)"`
	Avg   float64 `json:"avg"`
	P90   float64 `json:"p(90)"`
	P95   float64 `json:"p(95)"`
	P99   float64 `json:"p(99)"`
	P999  float64 `json:"p(99.9)"`
	Max   float64 `json:"max"`
	Rate  float64 `json:"rate,omitempty"`
	Count float64 `json:"count,omitempty"`
}

type Metric struct {
	Values Values `json:"values"`
}

// for k6 output, but also used by fortio for common charts for common fields
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

func (m Metric) IsZero() bool { return m.Values.Avg == 0 && m.Values.Max == 0 }

// json file output fields
type goTestEvent struct {
	Time    string `json:"Time"`
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test,omitempty"`
	Output  string `json:"Output,omitempty"`
}

const (
	inputPath = "./test_report_perf.json"
)

// Variant comparison aggregation for pubsub tests & can be expanded on in the
// future for other apis/tests. used to compare multiple variants of the test.
type variantComparison struct {
	baseName string
	outDir   string
	labels   []string
	runners  []Runner
}

var pubsubComparisons = make(map[string]variantComparison)

// create charts based on perf json output
func main() {
	// TODO: cassie update inputPath once automated & running in CI
	f, err := os.Open(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening %s: %v\n", inputPath, err)
		os.Exit(1)
	}
	defer f.Close()

	// used for local debugging of charts
	debugEnabled = strings.TrimSpace(os.Getenv("CHARTS_DEBUG")) != ""
	if debugEnabled {
		debugf("reading %s", inputPath)
	}

	// TODO: cassie update this once automated based on release version
	baseOutputDir := filepath.Join("charts", "master")
	if err = os.MkdirAll(baseOutputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating charts directory %s: %v\n", baseOutputDir, err)
		os.Exit(1)
	}

	// one level up - look at which perf tests are k6 vs fortio based dynamically bc data/charts differ btw the 2
	testModes = buildTestModes("..")
	if debugEnabled {
		debugf("test modes: %v", testModes)
	}
	scanner := bufio.NewScanner(f)
	var (
		currentTest     string
		currentPkg      string
		skipByTest      = make(map[string]bool)
		jsonBuilder     strings.Builder
		resourceByTest  = make(map[string]*ResourceUsage)
		collecting      bool
		collectingSuite PerfSuite
	)

	for scanner.Scan() {
		var ev goTestEvent
		if err = json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			// bad line in the json — just skip it...
			continue
		}

		switch ev.Action {
		// Marks the start of a subtest. Reset any partial json we may
		// have been collecting for a previous test
		case "run":
			if ev.Test != "" {
				// If we were still collecting for the previous test, finalize it now
				if collecting && currentTest != "" {
					final := finalizeCollectedJSON(jsonBuilder.String())
					if final != "" {
						switch collectingSuite {
						case SuiteK6:
							processK6Summary(final, currentTest, currentPkg, baseOutputDir)
						case SuiteFortio:
							processFortioSummary(final, currentTest, currentPkg, baseOutputDir)
						default:
							if debugEnabled {
								debugf("no suite for %s.%s. Dropping collected JSON...", currentPkg, currentTest)
							}
						}
					}
				}
				// reset per-test state
				collecting = false
				collectingSuite = SuiteUnknown
				currentTest = ev.Test
				currentPkg = ev.Package
				jsonBuilder.Reset()
				// Skip tests that don't make sense to chart:
				if strings.HasPrefix(currentTest, "TestParamsOpts") {
					skipByTest[currentTest] = true
					if debugEnabled {
						debugf("skipped test: pkg=%s test=%s", currentPkg, currentTest)
					}
					continue
				}

				if apiName, transport, ok := classifyAPIAndTransport(currentPkg, currentTest); ok {
					outDir := filepath.Join(baseOutputDir, apiName)
					if transport != "" {
						outDir = filepath.Join(outDir, transport)
					}
					err = os.MkdirAll(outDir, 0o755)
					if err != nil {
						fmt.Fprintf(os.Stderr, "error creating output directory %s: %v\n", outDir, err)
					} else if debugEnabled {
						debugf("classified: pkg=%s test=%s outDir=%s", currentPkg, currentTest, outDir)
					}
				} else if debugEnabled {
					debugf("unclassified: pkg=%s test=%s (untracked api)", currentPkg, currentTest)
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

			// Always try to parse resource usage lines
			// TODO: cassie add a GH step to verify that this is logged in newly added perf tests
			if usage, ok := parseResourceUsageLine(ev.Output); ok {
				ru := resourceByTest[currentTest]
				if ru == nil {
					ru = &ResourceUsage{}
					resourceByTest[currentTest] = ru
				}
				if usage.resource == "app" {
					ru.AppCPUm = usage.cpuMilli
					ru.AppMemMB = usage.memMB
				} else if usage.resource == "sidecar" {
					ru.SidecarCPUm = usage.cpuMilli
					ru.SidecarMemMB = usage.memMB
				}
				if !ru.Drawn && ru.AppCPUm > 0 && ru.SidecarCPUm > 0 {
					// draw charts once we have the resource data
					apiName, transport, ok := classifyAPIAndTransport(currentPkg, currentTest)
					if ok {
						// specific to bulk pubsub dir
						if strings.Contains(ev.Package, "pubsub") && strings.Contains(ev.Package, "bulk") {
							apiName = "pubsub/bulk"
						}
						outDir := filepath.Join(baseOutputDir, apiName)
						if transport != "" {
							outDir = filepath.Join(outDir, transport)
						}
						err = os.MkdirAll(outDir, 0o755)
						if err != nil {
							fmt.Fprintf(os.Stderr, "error creating output directory %s: %v\n", outDir, err)
						}
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
				if rs.resource == "target" {
					ru.TargetRestarts = rs.count
				} else if rs.resource == "tester" {
					ru.TesterRestarts = rs.count
				}
			}
			// If already collecting, just append the line & strip backticks from k6 logs
			// confirm mode (k6 vs fortio) from collecting suite (from test files) & json validation
			if collecting {
				line := strings.ReplaceAll(ev.Output, "`", "")
				lowerLine := strings.ToLower(line)
				// for now, skipping logging the 'baseline' runs... only charting the last one in those cases
				// If we see a new json start marker while collecting, restart the buffer
				if collectingSuite == SuiteK6 && (strings.Contains(lowerLine, "\"pass\":") || strings.Contains(lowerLine, "\"runnersResults\"")) {
					if debugEnabled {
						debugf("restart collect (k6 new payload): pkg=%s test=%s", currentPkg, currentTest)
					}
					jsonBuilder.Reset()
					jsonBuilder.WriteString("{\n")
					jsonBuilder.WriteString(line)
					continue
				}
				if collectingSuite == SuiteFortio && strings.Contains(lowerLine, "\"runtype\"") {
					jsonBuilder.Reset()
					jsonBuilder.WriteString("{\n")
					jsonBuilder.WriteString(line)
					continue
				}
				jsonBuilder.WriteString("\n")
				jsonBuilder.WriteString(line)
				continue
			}
			// Not collecting yet, so decide start & determine if its k6 vs fortio from testModes
			baseName := currentTest
			if idx := strings.Index(baseName, "/"); idx != -1 {
				baseName = baseName[:idx]
			}
			if i := strings.Index(baseName, ":_"); i != -1 {
				baseName = baseName[:i]
			}

			testModeFromTest := strings.ToLower(strings.TrimSpace(testModes[baseName])) // k6/fortio
			lower := strings.ToLower(ev.Output)
			// Start K6 collection when we see the summary content begin & confirm json matches testModeFromTest
			if testModeFromTest == "k6" && (strings.Contains(lower, "\"pass\":") || strings.Contains(lower, "\"runnersresults\"")) {
				collecting = true
				collectingSuite = SuiteK6
				jsonBuilder.Reset()
				// Seed opening brace since '{' which is on a prior line that is user generated, so avoid grepping for user generated t.log lines...
				jsonBuilder.WriteString("{\n")
				line := strings.ReplaceAll(ev.Output, "`", "")
				jsonBuilder.WriteString(line)
				continue
			}
			// Start Fortio collection when we see the first field like "RunType": & confirm json matches testModeFromTest
			if testModeFromTest == "fortio" && strings.Contains(lower, "\"runtype\"") {
				collecting = true
				collectingSuite = SuiteFortio
				jsonBuilder.Reset()
				jsonBuilder.WriteString("{\n")
				line := strings.ReplaceAll(ev.Output, "`", "")
				jsonBuilder.WriteString(line)
				continue
			}
			continue

		// If this is the package-level completion event (no Test field), then
		// we know we've reached the end of all API specific perf tests
		// ex: {"Time":"...","Action":"pass","Package":"github.com/dapr/dapr/tests/perf/workflows","Elapsed":...}
		case "pass", "fail", "skip":
			// If this marks the end of the current test, finalize collection
			if ev.Test == currentTest && collecting {
				final := finalizeCollectedJSON(jsonBuilder.String())
				if final != "" {
					switch collectingSuite {
					case SuiteK6:
						processK6Summary(final, currentTest, currentPkg, baseOutputDir)
					case SuiteFortio:
						processFortioSummary(final, currentTest, currentPkg, baseOutputDir)
					default:
					}
				}
				collecting = false
				collectingSuite = SuiteUnknown
				jsonBuilder.Reset()
			} else if ev.Test == "" {
				// pkg-level completion, reset
				if collecting {
					fmt.Fprintf(os.Stderr, "Package %s completed while still collecting JSON for test %s\n", ev.Package, currentTest)
					collecting = false
					collectingSuite = SuiteUnknown
					jsonBuilder.Reset()
				}
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
		makeTailLatencyChart(agg, prefix, result.outDir)

		// comparison chart across runs (skipped if only 1 run)
		makeCombinedCharts(result.runners, prefix, result.outDir)
	}

	// PubSub variant comparison charts bc it runs with a t.run with multiple inputs here:
	// tests/perf/pubsub_bulk_publish_http/pubsub_bulk_publish_http_test.go
	for _, cmp := range pubsubComparisons {
		if len(cmp.runners) < 2 {
			continue
		}
		_ = os.MkdirAll(cmp.outDir, 0o755)
		makeVariantComparisonCharts(cmp.labels, cmp.runners, sanitizeName(cmp.baseName), cmp.outDir)
	}

	log.Printf("Generated charts for performance tests in %s\n", baseOutputDir)
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

// trim anything after the last closing brace
func finalizeCollectedJSON(buf string) string {
	buf = strings.TrimSpace(strings.ReplaceAll(buf, "`", ""))
	if buf == "" {
		return ""
	}
	// Keep everything from the first '{' to the last '}' inclusive
	start := strings.Index(buf, "{")
	end := strings.LastIndex(buf, "}")
	if start == -1 || end == -1 || start >= end {
		return ""
	}
	segment := strings.TrimSpace(buf[start : end+1])
	return segment
}
