/*
Copyright 2023 The Dapr Authors
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

package summary

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/dapr/kit/logger"
)

const (
	// testNameSeparator is the default character used by go test to separate between suitecases under the same test func.
	testNameSeparator = "/"
)

var log = logger.NewLogger("tests.runner.summary")

func sanitizeTestName(testName string) string {
	return strings.ReplaceAll(testName, testNameSeparator, "_")
}

func filePath(prefix, testName string) string {
	return fmt.Sprintf("%s_summary_table_%s.json", prefix, sanitizeTestName(testName))
}

// Table is primarily used as a source of arbitrary test output. Tests can send output to the summary table and later flush them.
// when flush is called so the table is serialized into a file that contains the test name on it, so you should use one table per test.
// the table output is typically used as a github enhanced job summary.
// see more: https://github.blog/2022-05-09-supercharging-github-actions-with-job-summaries/
type Table struct {
	Test string      `json:"test"`
	Data [][2]string `json:"data"`
}

// Output adds a pair to the table data pairs.
func (t *Table) Output(header, value string) *Table {
	t.Data = append(t.Data, [2]string{header, value})
	return t
}

// OutputInt same as output but converts from int to string.
func (t *Table) OutputInt(header string, value int) *Table {
	return t.Output(header, strconv.Itoa(value))
}

// OutputFloat64 same as output but converts from float64 to string.
func (t *Table) OutputFloat64(header string, value float64) *Table {
	return t.Outputf(header, "%f", value)
}

// Outputf same as output but uses a formatter.
func (t *Table) Outputf(header, format string, params ...any) *Table {
	return t.Output(header, fmt.Sprintf(format, params...))
}

// Service is a shortcut for .Output("Service")
func (t *Table) Service(serviceName string) *Table {
	return t.Output("Service", serviceName)
}

// Service is a shortcut for .Output("Client")
func (t *Table) Client(clientName string) *Table {
	return t.Output("Client", clientName)
}

// CPU is a shortcut for .Outputf("CPU", "%vm")
func (t *Table) CPU(cpu int64) *Table {
	return t.Outputf("CPU", "%vm", cpu)
}

// Memory is a shortcut for .Outputf("Memory", "%vm")
func (t *Table) Memory(cpu float64) *Table {
	return t.Outputf("Memory", "%vMb", cpu)
}

// SidecarCPU is a shortcut for .Outputf("Sidecar CPU", "%vm")
func (t *Table) SidecarCPU(cpu int64) *Table {
	return t.Outputf("Sidecar CPU", "%vm", cpu)
}

// BaselineLatency is a shortcut for Outputf("Baseline latency avg", "%2.fms")
func (t *Table) BaselineLatency(latency float64) *Table {
	return t.Outputf("Baseline latency avg", "%.2fms", latency)
}

// DaprLatency is a shortcut for Outputf("Dapr latency avg", "%2.fms")
func (t *Table) DaprLatency(latency float64) *Table {
	return t.Outputf("Dapr latency avg", "%.2fms", latency)
}

// AddedLatency is a shortcut for Outputf("Added latency avg", "%2.fms")
func (t *Table) AddedLatency(latency float64) *Table {
	return t.Outputf("Added latency avg", "%.2fms", latency)
}

// SidecarMemory is a shortcut for .Outputf("Sidecar Memory", "%vm")
func (t *Table) SidecarMemory(cpu float64) *Table {
	return t.Outputf("Sidecar Memory", "%vMb", cpu)
}

// Restarts is a shortcut for .OutputInt("Restarts")
func (t *Table) Restarts(restarts int) *Table {
	return t.OutputInt("Restarts", restarts)
}

// ActualQPS is a short for .Outputf("QPS", ".2f")
func (t *Table) ActualQPS(qps float64) *Table {
	return t.Outputf("Actual QPS", "%.2f", qps)
}

// QPS is a short for .OutputInt("QPS")
func (t *Table) QPS(qps int) *Table {
	return t.OutputInt("QPS", qps)
}

// P90 is a short for .Outputf("P90", "%2.fms")
func (t *Table) P90(p90 float64) *Table {
	return t.Outputf("P90", "%.2fms", p90)
}

// P90 is a short for .Outputf("P90", "%2.fms")
func (t *Table) P99(p99 float64) *Table {
	return t.Outputf("P99", "%.2fms", p99)
}

// QPS is a short for .OutputInt("QPS")
func (t *Table) Params(p perf.TestParameters) *Table {
	return t.QPS(p.QPS).
		OutputInt("Client connections", p.ClientConnections).
		Output("Target endpoint", p.TargetEndpoint).
		Output("Test duration", p.TestDuration).
		OutputInt("PayloadSizeKB", p.PayloadSizeKB)
}

type unit string

const (
	millisecond unit = "ms"
)

var unitFormats = map[unit]string{
	millisecond: "%.2fms",
}

// OutputK6Trend outputs the given k6trend using the given prefix.
func (t *Table) OutputK6Trend(prefix string, unit unit, trend loadtest.K6TrendMetric) *Table {
	t.Outputf(fmt.Sprintf("%s MAX", prefix), unitFormats[unit], trend.Values.Max)
	t.Outputf(fmt.Sprintf("%s MIN", prefix), unitFormats[unit], trend.Values.Min)
	t.Outputf(fmt.Sprintf("%s AVG", prefix), unitFormats[unit], trend.Values.Avg)
	t.Outputf(fmt.Sprintf("%s MED", prefix), unitFormats[unit], trend.Values.Med)
	t.Outputf(fmt.Sprintf("%s P90", prefix), unitFormats[unit], trend.Values.P90)
	t.Outputf(fmt.Sprintf("%s P95", prefix), unitFormats[unit], trend.Values.P95)
	return t
}

const (
	p90Index = 2
	p99Index = 3
)

const (
	secondToMillisecond = 1000
)

// OutputFortio summarize the fortio results.
func (t *Table) OutputFortio(result perf.TestResult) *Table {
	return t.
		P90(result.DurationHistogram.Percentiles[p90Index].Value*secondToMillisecond).
		P99(result.DurationHistogram.Percentiles[p99Index].Value*secondToMillisecond).
		OutputInt("2xx", result.RetCodes.Num200).
		OutputInt("4xx", result.RetCodes.Num400).
		OutputInt("5xx", result.RetCodes.Num500)
}

// OutputK6 summarize the K6 results for each runner.
func (t *Table) OutputK6(k6results []*loadtest.K6RunnerMetricsSummary) *Table {
	for i, result := range k6results {
		t.OutputInt(fmt.Sprintf("[Runner %d]: VUs Max", i), result.Vus.Values.Max)
		t.OutputFloat64(fmt.Sprintf("[Runner %d]: Iterations Count", i), result.Iterations.Values.Count)
		t.OutputK6Trend(fmt.Sprintf("[Runner %d]: Req Duration", i), millisecond, result.HTTPReqDuration)
		t.OutputK6Trend(fmt.Sprintf("[Runner %d]: Req Waiting", i), millisecond, result.HTTPReqWaiting)
		t.OutputK6Trend(fmt.Sprintf("[Runner %d]: Iteration Duration", i), millisecond, result.IterationDuration)
	}
	return t
}

// Flush saves the summary into the disk using the desired format.
func (t *Table) Flush() error {
	bts, err := json.Marshal(t)
	if err != nil {
		log.Errorf("error when marshalling table %s: %v", t.Test, err)
		return err
	}

	filePrefixOutput, ok := os.LookupEnv("TEST_OUTPUT_FILE_PREFIX")
	if !ok {
		filePrefixOutput = "./test_report"
	}

	err = os.WriteFile(filePath(filePrefixOutput, t.Test), bts, os.ModePerm)
	if err != nil {
		log.Errorf("error when saving table %s: %v", t.Test, err)
		return err
	}
	return nil
}

// ForTest returns a table ready to be written for the given test.
func ForTest(tst *testing.T) *Table {
	return &Table{
		Test: tst.Name(),
		Data: [][2]string{},
	}
}
