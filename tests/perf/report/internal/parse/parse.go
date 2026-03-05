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

package parse

import "strings"

// Provide shared types and utilities for reading `go test -json`
// perf output (Fortio and k6 result formats) used by both the chart generator
// and the highlights generator.

// TestEvent is one line of `go test -json` output.
type TestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test,omitempty"`
	Output  string `json:"Output,omitempty"`
}

// Fortio perf result with percentiles + histogram
type PercentilePoint struct {
	Percentile float64 `json:"Percentile"`
	Value      float64 `json:"Value"`
}

// DurationHistogram holds the full latency distribution from a Fortio run.
type DurationHistogram struct {
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
	Percentiles []PercentilePoint `json:"Percentiles,omitempty"`
}

// FortioResult is the JSON output of a Fortio perf run.
type FortioResult struct {
	RunType            string             `json:"RunType"`
	RequestedQPS       string             `json:"RequestedQPS"`
	RequestedDuration  string             `json:"RequestedDuration"`
	ActualQPS          float64            `json:"ActualQPS"`
	ActualDuration     float64            `json:"ActualDuration"`
	NumThreads         int                `json:"NumThreads"`
	DurationHistogram  DurationHistogram  `json:"DurationHistogram"`
	Percentiles        []PercentilePoint  `json:"Percentiles"`
	ErrorsDurationHist *DurationHistogram `json:"ErrorsDurationHistogram,omitempty"`
	Dapr               string             `json:"Dapr,omitempty"`
	URL                string             `json:"URL,omitempty"`
	RetCodes           map[string]int     `json:"RetCodes,omitempty"`
	Sizes              map[string]any     `json:"Sizes,omitempty"`
	HeaderSizes        map[string]any     `json:"HeaderSizes,omitempty"`
	ConnectionStats    map[string]any     `json:"ConnectionStats,omitempty"`
	IPCountMap         map[string]int     `json:"IPCountMap,omitempty"`
}

// k6 types

// Values holds the statistical values for a k6 metric.
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

// Metric wraps the statistical Values for a single k6 metric.
type Metric struct {
	Values Values `json:"values"`
}

// IsZero reports whether the metric contains no meaningful data.
func (m Metric) IsZero() bool { return m.Values.Avg == 0 && m.Values.Max == 0 }

// Runner holds all k6 metrics for one test run.
// Fortio results are converted into this format via convertFortioPerfToRunner
// so that both suites can share the same chart functions.
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

// K6Summary is the top-level JSON object output by a k6 perf test.
type K6Summary struct {
	Pass           bool     `json:"pass"`
	RunnersResults []Runner `json:"runnersResults"`
}

// add missing closing braces/brackets if we detect more openings than closings.
// fix for logs where the JSON object may be truncated by line buffering
func RepairJSONClosers(s string) string {
	openCurly := strings.Count(s, "{")
	closeCurly := strings.Count(s, "}")
	openSquare := strings.Count(s, "[")
	closeSquare := strings.Count(s, "]")
	var b strings.Builder
	b.WriteString(s)
	// Close arrays first, then objects since most blocks are objects containing arrays.
	for range openSquare - closeSquare {
		b.WriteString("]")
	}
	for range openCurly - closeCurly {
		b.WriteString("}")
	}
	return b.String()
}
