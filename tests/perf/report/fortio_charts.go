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

// Fortio perf result with percentiles + histogram
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

type FortioResult struct {
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

// processFortioSummary parses Fortio style json perf output & converts them into a Runner
// then stores them using the same aggregation mechanism
func processFortioSummary(objJSON, testName, pkg, baseOutputDir string) {
	raw := strings.TrimSpace(objJSON)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return
	}

	candidate := raw
	if !json.Valid([]byte(candidate)) {
		if debugEnabled {
			debugf("invalid JSON: %s", candidate)
		}
		s := sanitizePerfJSON(raw)
		s = repairJSONClosers(s)
		if json.Valid([]byte(s)) {
			candidate = s
		} else {
			fmt.Fprintf(os.Stderr, "fortio JSON parse error for %s.\nJSON: %s\n", testName, candidate)
			return
		}
	}

	var res FortioResult
	if err := json.Unmarshal([]byte(candidate), &res); err != nil {
		s := sanitizePerfJSON(candidate)
		s = repairJSONClosers(s)
		var retry FortioResult
		if err = json.Unmarshal([]byte(s), &retry); err != nil {
			fmt.Fprintf(os.Stderr, "fortio unmarshal error after retry for %s: %v\nJSON: %s\n", testName, err, candidate)
			return
		}
		res = retry
	}

	if len(res.Percentiles) == 0 && res.DurationHistogram.Count == 0 {
		fmt.Fprintf(os.Stderr, "no percentiles or duration histogram for %s.\nJSON: %s\n", testName, candidate)
		return
	}
	r := convertFortioPerfToRunner(res, res.RunType)
	filePrefix := sanitizeName(testName) // preserve subtest or run suffix for per-run charts, ex: b10_s1KB_bulk

	// Resolve output context (api/transport/outDir/keys)
	info, ok := prepareOutputInfo(pkg, testName, baseOutputDir)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown API or transport for %s.\nfortio JSON: %s\n", testName, candidate)
		return
	}
	storeRunner(r, info)

	makeQPSChart(res, filePrefix, info.outDir)
	makeDurationRequestedVsActualChart(res, filePrefix, info.outDir)
	makeDurationHistogramCharts(res, filePrefix, info.outDir)
	makeConnectionStatsChart(res, filePrefix, info.outDir)
	makeSizesCharts(res, filePrefix, info.outDir)
}

// map a Fortio perf result (percentiles + histogram) into the Runner format
// which is used for common charts btw the k6 + fortio suites
func convertFortioPerfToRunner(res FortioResult, runType string) Runner {
	getPct := func(p float64) float64 {
		for _, pt := range res.DurationHistogram.Percentiles {
			if int(pt.Percentile*10) == int(p*10) || pt.Percentile == p {
				return pt.Value
			}
		}
		return 0
	}
	computeSuccessRate := func() float64 {
		if len(res.RetCodes) == 0 {
			return -1 // no data, don't chart
		}
		total := 0
		success := 0
		// grpc data has serving in it vs http gives 204/200
		runTypeLower := strings.ToLower(strings.TrimSpace(runType))
		for k, v := range res.RetCodes {
			total += v
			lk := strings.ToLower(strings.TrimSpace(k))
			if runTypeLower == "grpc" {
				if lk == "serving" {
					success += v
				}
			} else if runTypeLower == "http" {
				if strings.HasPrefix(k, "2") {
					success += v
				}
			} else {
				// Unspecified runType, also valid
				if strings.HasPrefix(k, "2") || lk == "serving" {
					success += v
				}
			}
		}
		if total == 0 {
			return -1
		}
		return float64(success) / float64(total)
	}

	med := getPct(50)
	p75 := getPct(75)
	p90 := getPct(90)
	p95 := getPct(95)
	// higher percentiles if available (not always available)
	p99 := getPct(99.0)
	p999 := getPct(99.9)
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
			P75:   p75,
			Avg:   avg,
			P90:   p90,
			P95:   p95,
			P99:   p99,
			P999:  p999,
			Max:   max,
			Rate:  rate,
			Count: count,
		}
	}

	durationSecs := normalizeActualDurationSeconds(res.ActualDuration)
	getMapNumber := func(m map[string]interface{}, key string) float64 {
		if m == nil {
			return 0
		}
		for k, v := range m {
			if !strings.EqualFold(key, k) {
				continue
			}
			if f, ok := v.(float64); ok {
				return f
			}
		}
		return 0
	}
	recvBytesTotal := getMapNumber(res.Sizes, "Sum") + getMapNumber(res.HeaderSizes, "Sum")
	var recvRate float64
	if durationSecs > 0 && recvBytesTotal > 0 {
		recvRate = recvBytesTotal / durationSecs
	}
	// not in fortio data, so leave 0
	sentBytesTotal := 0.0
	var sentRate float64

	// only fill in the common vals to be used for common charts
	var checks Metric
	if checksRate >= 0 {
		checks = Metric{Values: Values{Rate: checksRate}}
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
		Checks:            checks,
		VUsMax:            Metric{Values: Values{}},
		DataReceived:      Metric{Values: Values{Rate: recvRate, Count: recvBytesTotal}},
		DataSent:          Metric{Values: Values{Rate: sentRate, Count: sentBytesTotal}},
	}
}

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
	// vals around 6e10 are likely nanoseconds (~60s)
	if actual > 1e6 {
		return actual / 1e9
	}
	return actual
}

// QPS requested vs actual
func makeQPSChart(res FortioResult, prefix, outDir string) {
	req := parseRequestedQPS(res.RequestedQPS)
	act := res.ActualQPS

	p := plot.New()
	p.Title.Text = fmt.Sprintf("QPS – Requested vs Actual (threads=%d)", res.NumThreads)
	p.Y.Label.Text = "QPS"

	values := plotter.Values{req, act}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = colorRGBA(54, 162, 235, 255) // blue
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
func makeDurationRequestedVsActualChart(res FortioResult, prefix, outDir string) {
	req := parseRequestedDurationSeconds(res.RequestedDuration)
	act := normalizeActualDurationSeconds(res.ActualDuration)

	p := plot.New()
	p.Title.Text = fmt.Sprintf("Duration – Requested vs Actual (s) (threads=%d)", res.NumThreads)
	p.Y.Label.Text = "Seconds"

	values := plotter.Values{req, act}
	bar, _ := plotter.NewBarChart(values, vg.Points(55))
	bar.Color = colorRGBA(255, 159, 64, 255) // orange
	p.Add(bar)

	p.X.Min = -0.5
	p.X.Max = 1.5
	p.X.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "Requested (s)"},
		{Value: 1, Label: "Actual (s)"},
	})

	p.Save(7*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_duration_requested_vs_actual.png"))
}

// histogram charts: percent per bin && count per bin
func makeDurationHistogramCharts(res FortioResult, prefix, outDir string) {
	data := res.DurationHistogram.Data
	if len(data) == 0 {
		return
	}
	labels := make([]plot.Tick, 0, len(data))
	percValues := make(plotter.Values, 0, len(data))
	countValues := make(plotter.Values, 0, len(data))
	for _, b := range data {
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

	// acct for diff number of bins - for actor tests, there's a ton, so need a larger chart otherwise there is overlap
	nBins := len(percValues)
	barWidth := vg.Points(18)
	xMin := -0.2
	xMax := float64(nBins) - 0.8
	chartWidth := 16 * vg.Inch
	if nBins <= 8 {
		barWidth = vg.Points(20)
		xMin = -0.5
		xMax = float64(nBins) - 0.5
		chartWidth = 12 * vg.Inch
	}

	// Percent chart
	p1 := plot.New()
	p1.Title.Text = "Latency Histogram – Percent per Bin"
	p1.X.Label.Text = "Latency bucket (ms)"
	p1.Y.Label.Text = "Percent (%)"
	bar1, _ := plotter.NewBarChart(percValues, barWidth)
	bar1.Color = colorRGBA(75, 192, 192, 255) // teal-like blue
	p1.Add(bar1)
	p1.X.Min = xMin
	p1.X.Max = xMax
	p1.X.Tick.Marker = plot.ConstantTicks(labels)
	p1.Save(chartWidth, 4*vg.Inch, filepath.Join(outDir, prefix+"_histogram_percent.png"))

	// Count chart
	p2 := plot.New()
	p2.Title.Text = "Latency Histogram – Count per Bin"
	p2.X.Label.Text = "Latency bucket (ms)"
	p2.Y.Label.Text = "Count"
	bar2, _ := plotter.NewBarChart(countValues, barWidth)
	bar2.Color = colorRGBA(153, 102, 255, 255) // purple
	p2.Add(bar2)
	p2.X.Min = xMin
	p2.X.Max = xMax
	p2.X.Tick.Marker = plot.ConstantTicks(labels)
	p2.Save(chartWidth, 4*vg.Inch, filepath.Join(outDir, prefix+"_histogram_count.png"))
}

// ConnectionStats min/avg/max chart if available
func makeConnectionStatsChart(res FortioResult, prefix, outDir string) {
	if len(res.ConnectionStats) == 0 {
		return
	}

	get := func(name string) float64 {
		name = strings.ToLower(name)
		for k, v := range res.ConnectionStats {
			if !strings.EqualFold(k, name) {
				continue
			}
			if f, ok := v.(float64); ok {
				return f
			}
			return 0
		}
		return 0
	}
	min := get("min")
	avg := get("avg")
	max := get("max")
	stddev := get("stddev")
	count := get("count")
	p := plot.New()
	if count > 0 {
		p.Title.Text = fmt.Sprintf("Connection Stats (seconds) – Count: %.0f", count)
	} else {
		p.Title.Text = "Connection Stats (seconds)"
	}
	unit := "Seconds"
	scale := 1.0
	type labelValue struct {
		label string
		val   float64
	}
	lvs := make([]labelValue, 0, 5)
	if min != 0 {
		lvs = append(lvs, labelValue{"Min", min})
	}
	if avg != 0 {
		lvs = append(lvs, labelValue{"Avg", avg})
	}
	if max != 0 {
		lvs = append(lvs, labelValue{"Max", max})
	}
	if stddev != 0 {
		lvs = append(lvs, labelValue{"StdDev", stddev})
	}
	maxVal := 0.0
	for _, lv := range lvs {
		if lv.val > maxVal {
			maxVal = lv.val
		}
	}
	// adjust units accordingly
	if maxVal < 0.0005 {
		unit = "Microseconds"
		scale = 1e6
	} else if maxVal < 0.01 {
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
	bar.Color = colorRGBA(102, 194, 165, 255) // greenish
	p.Add(bar)
	p.X.Min = -0.5
	p.X.Max = float64(len(values)) - 0.5
	p.X.Tick.Marker = plot.ConstantTicks(ticks)
	p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_connection_stats.png"))
}

// Payload Size && HeaderSize charts (min/avg/max)
func makeSizesCharts(res FortioResult, prefix, outDir string) {
	type stats struct {
		min, avg, max float64
		stddev        float64
		count         float64
		ok            bool
	}
	// extract min/avg/max/stddev/count
	extract := func(m map[string]interface{}) stats {
		if m == nil || len(m) == 0 {
			return stats{}
		}
		lowerNum := func(key string) float64 {
			key = strings.ToLower(key)
			for kk, vv := range m {
				if strings.EqualFold(key, kk) {
					if f, ok := vv.(float64); ok {
						return f
					}
					break
				}
			}
			return 0
		}
		min := lowerNum("min")
		avg := lowerNum("avg")
		max := lowerNum("max")
		stddev := lowerNum("stddev")
		count := lowerNum("count")
		return stats{min: min, avg: avg, max: max, stddev: stddev, count: count, ok: min != 0 || avg != 0 || max != 0 || stddev != 0}
	}
	// payload chart
	if s := extract(res.Sizes); s.ok {
		p := plot.New()
		if s.count > 0 {
			p.Title.Text = fmt.Sprintf("Payload Size (bytes) – Count: %.0f", s.count)
		} else {
			p.Title.Text = "Payload Size (bytes)"
		}
		p.Y.Label.Text = "Bytes"
		// leave out Sum to avoid dwarfing other bars
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
		bar.Color = colorRGBA(75, 192, 192, 255) // teal blue
		p.Add(bar)
		p.X.Min = -0.5
		p.X.Max = float64(len(values)) - 0.5
		p.X.Tick.Marker = plot.ConstantTicks(labels)
		p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_payload_size.png"))
	}
	// header chart
	if hs := extract(res.HeaderSizes); hs.ok {
		p := plot.New()
		if hs.count > 0 {
			p.Title.Text = fmt.Sprintf("Header Size (bytes) – Count: %.0f", hs.count)
		} else {
			p.Title.Text = "Header Size (bytes)"
		}
		p.Y.Label.Text = "Bytes"
		// leave out Sum to avoid dwarfing other bars
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
		bar.Color = colorRGBA(255, 159, 64, 255) // orange
		p.Add(bar)
		p.X.Min = -0.5
		p.X.Max = float64(len(values)) - 0.5
		p.X.Tick.Marker = plot.ConstantTicks(labels)
		p.Save(8*vg.Inch, 4*vg.Inch, filepath.Join(outDir, prefix+"_header_size.png"))
	}
}
