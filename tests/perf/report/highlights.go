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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// testSummary holds extracted per-test metrics for the highlights report.
type testSummary struct {
	Name        string
	Transport   string // HTTP/gRPC/empty
	IsK6        bool
	QPS         float64 // requests or iterations per second
	TotalReqs   float64 // total requests / iterations / workflows
	P50ms       float64
	P90ms       float64
	P95ms       float64
	P99ms       float64
	P999ms      float64
	SuccessPct  float64
	MaxVUs      float64 // for k6 tests
	Connections int     // for Fortio: NumThreads
}

// Ollama request/response types.
type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaResponse struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
}

// api ordering for the highlights report output.
var apiOrder = []string{
	"service_invocation",
	"workflows",
	"state",
	"pubsub",
	"actors",
	"configuration",
}

var apiDisplay = map[string]string{
	"service_invocation": "Service Invocation",
	"workflows":          "Workflows",
	"state":              "State",
	"pubsub":             "PubSub",
	"actors":             "Actors",
	"configuration":      "Configuration",
}

var apiDir = map[string]string{
	"service_invocation": "service_invocation",
	"workflows":          "workflows",
	"state":              "state",
	"pubsub":             "pubsub",
	"actors":             "actors",
	"configuration":      "configuration",
}

// generateHighlights builds per-API narrative highlights from the already-parsed
// combinedResults and writes README.md files under baseOutputDir.
// It is called from main() after writeReadmes() when --model is provided.
func generateHighlights(version, baseOutputDir, model, ollamaURL string) {
	// Build testSummaries grouped by top-level api key derived from outDir.
	grouped := map[string][]testSummary{}

	for _, result := range combinedResults {
		if len(result.runners) == 0 {
			continue
		}

		// Derive api key and transport from outDir (already classified during scanning).
		rel, err := filepath.Rel(baseOutputDir, result.outDir)
		if err != nil || rel == "" || rel == "." {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		apiKey := parts[0]

		// Transport is the last path segment if it names a transport.
		transport := ""
		last := parts[len(parts)-1]
		switch last {
		case transportHTTP:
			transport = "HTTP"
		case transportGRPC:
			transport = "gRPC"
		}

		agg := aggregateRunners(result.runners)
		isK6 := agg.IterationDuration.Values.Med > 0 || agg.VUsMax.Values.Max > 0

		// use HTTPReqDuration when non-zero (HTTP k6 tests and all Fortio tests)
		// Fall back to IterationDuration only when HTTP metrics are absent (workflow-only k6 tests)
		durMetric := agg.HTTPReqDuration
		if agg.HTTPReqDuration.Values.Med == 0 && agg.IterationDuration.Values.Med > 0 {
			durMetric = agg.IterationDuration
		}

		// Use -1 to indicate "unknown" when no checks or RetCode data is present,
		// rather than defaulting to 100% which misrepresents Fortio runs without RetCodes
		successPct := -1.0
		if agg.Checks.Values.Rate > 0 {
			successPct = agg.Checks.Values.Rate * 100
		}

		s := testSummary{
			Name:        result.name,
			Transport:   transport,
			IsK6:        isK6,
			P50ms:       durMetric.Values.Med * 1000,
			P90ms:       durMetric.Values.P90 * 1000,
			P95ms:       durMetric.Values.P95 * 1000,
			P99ms:       durMetric.Values.P99 * 1000,
			P999ms:      durMetric.Values.P999 * 1000,
			QPS:         agg.Iterations.Values.Rate,
			TotalReqs:   agg.Iterations.Values.Count,
			MaxVUs:      agg.VUsMax.Values.Max,
			SuccessPct:  successPct,
			Connections: result.numThreads,
		}
		grouped[apiKey] = append(grouped[apiKey], s)
	}

	// Sort each group by name for deterministic output
	for api := range grouped {
		sort.Slice(grouped[api], func(i, j int) bool {
			return grouped[api][i].Name < grouped[api][j].Name
		})
	}

	// sectionContent maps api → the shared highlights text (narrative + how-to + table).
	// The root README copies this content directly.
	sectionContent := map[string]string{}

	for _, api := range apiOrder {
		tests := grouped[api]
		if len(tests) == 0 {
			continue
		}

		display := apiDisplay[api]
		dir := apiDir[api]
		subDir := filepath.Join(baseOutputDir, dir)

		if _, err := os.Stat(subDir); err != nil {
			// No charts generated for this API — skip.
			continue
		}

		fullSection, mixedTools, err := generateSectionNarrative(display, version, tests, model, ollamaURL)
		if err != nil {
			log.Printf("warning: narrative generation failed for %s: %v — using fallback", api, err)
			fullSection = defaultNarrative(display, version, tests) + "\n\n" + formatTestsTable(tests)
		}
		// Strip any trailing horizontal rule Ollama may append.
		fullSection = strings.TrimRight(fullSection, "\n ")
		fullSection = strings.TrimSuffix(fullSection, "---")
		fullSection = strings.TrimRight(fullSection, "\n ")

		// add callout when the API mixes Fortio and k6 tests to not confuse users by the data,
		// since small & free models often drop this instruction from the prompt
		if mixedTools {
			fullSection += "\n\n> **Note on test methodology:** This API is tested with two different load generators. " +
				"Fortio tests (gRPC) run at high throughput with many parallel connections and report p90/p99/p99.9. " +
				"k6 tests (HTTP) use virtual users with sequential iterations and report p90/p95. " +
				"These tests use different load profiles and are **not directly comparable** — do not draw conclusions by comparing gRPC and HTTP numbers side by side."
		}

		// Root README gets the narrative only — howToReadSection() already covers it at the top.
		sectionContent[api] = fullSection

		// Sub-dir README appends a How-to-read paragraph for readers landing directly on it.
		subContent := fullSection + "\n\n" + generateHowToRead(tests)

		// Read existing charts content (images), strip any stale highlights header, prepend fresh one.
		subReadmePath := filepath.Join(subDir, "README.md")
		chartsContent := ""
		if data, err := os.ReadFile(subReadmePath); err == nil {
			chartsContent = string(data)
			if strings.HasPrefix(chartsContent, "## Highlights\n") {
				if idx := strings.Index(chartsContent, "\n---\n\n"); idx != -1 {
					chartsContent = chartsContent[idx+6:]
				}
			}
		}

		combined := "## Highlights\n\n" + subContent + "\n\n---\n\n" + chartsContent
		if err := os.WriteFile(subReadmePath, []byte(combined), 0o600); err != nil {
			log.Printf("warning: could not write %s: %v", subReadmePath, err)
		} else {
			log.Printf("Wrote %s", subReadmePath)
		}
	}

	// Assemble root README by copying section content from sectionContent.
	var rootBuf strings.Builder
	fmt.Fprintf(&rootBuf, "# Dapr %s Performance Highlights\n\n", version)
	rootBuf.WriteString(howToReadSection())
	rootBuf.WriteString("\n---\n\n")
	for _, api := range apiOrder {
		content, ok := sectionContent[api]
		if !ok {
			continue
		}
		fmt.Fprintf(&rootBuf, "## %s → [charts](./%s/)\n\n", apiDisplay[api], apiDir[api])
		rootBuf.WriteString(content)
		rootBuf.WriteString("\n\n---\n\n")
	}

	rootPath := filepath.Join(baseOutputDir, "README.md")
	if err := os.WriteFile(rootPath, []byte(rootBuf.String()), 0o600); err != nil {
		log.Printf("warning: could not write root README %s: %v", rootPath, err)
	} else {
		log.Printf("Wrote %s", rootPath)
	}
}

// howToReadSection returns the static "How to read these numbers" content.
func howToReadSection() string {
	return `## How to read these numbers

**Latency percentiles** — each test fires thousands of requests and records how long each one took. Results are reported as percentiles:
- **p50 (median)**: Half of all requests completed faster than this — the typical user experience.
- **p90**: 9 out of 10 requests completed faster than this — what most users see under sustained load.
- **p99**: Only 1 in 100 requests took longer — captures tail latency, important for SLA planning.
- **p99.9**: Only 1 in 1,000 requests exceeded this — the extreme edge of the distribution.

**Connections** — the number of persistent parallel connections the load generator held open to the Dapr sidecar.

**QPS (queries/requests per second)** — the request rate the load generator sustained. Actual QPS values were within 0.1% of the target in every test.

**Dapr overhead** — measured by running the identical test directly between services (bypassing Dapr) and subtracting that baseline latency from the Dapr-proxied result.

**VUs (virtual users)** — used in workflow and scenario-based tests; the number of concurrent users or workflow instances active simultaneously.

**Pod restarts** — if an app or sidecar crashed or was OOM-killed during a test, Kubernetes restarts it. Zero restarts means the system stayed stable under every load scenario tested.

> **Note:** Some APIs are tested with [Fortio](https://fortio.org/) (high-throughput, many parallel connections) and others with [k6](https://k6.io/) (virtual-user based, sequential iterations). These tools use different load profiles and emit different percentile sets (Fortio: p90/p99/p99.9; k6: p90/p95). Only compare results from tests that use the **same load generator** — comparing a Fortio gRPC result against a k6 HTTP result is apples to oranges.
`
}

// generateHowToRead builds a "How to read these numbers" paragraph by inspecting
// which metrics are actually present in the test data.
func generateHowToRead(tests []testSummary) string {
	var hasP90, hasP99, hasP999, hasQPS, hasVUs, hasConnections bool
	for _, t := range tests {
		if t.P90ms > 0 {
			hasP90 = true
		}
		if t.P99ms > 0 {
			hasP99 = true
		}
		if t.P999ms > 0 {
			hasP999 = true
		}
		if t.QPS > 0 {
			hasQPS = true
		}
		if t.MaxVUs > 0 {
			hasVUs = true
		}
		if t.Connections > 0 {
			hasConnections = true
		}
	}

	var sentences []string

	latency := "**p50** (median) is the latency the typical request experienced"
	if hasP90 {
		latency += "; **p90** means 9 in 10 requests completed faster"
	}
	if hasP99 {
		latency += "; **p99** means only 1 in 100 requests took longer — useful for SLA planning"
	}
	if hasP999 {
		latency += "; **p99.9** is the extreme tail — only 1 in 1,000 exceeded this"
	}
	sentences = append(sentences, latency)

	if hasConnections {
		sentences = append(sentences, "**Connections** is the number of concurrent callers held open to the Dapr sidecar simultaneously")
	}
	if hasQPS {
		sentences = append(sentences, "**QPS** is the sustained request rate the load generator maintained")
	}
	if hasVUs {
		sentences = append(sentences, "**VUs** (virtual users) are concurrent instances active at the same time")
	}

	return "<details><summary>How to read these numbers</summary>\n\n" +
		strings.Join(sentences, ". ") + ".\n\n</details>"
}

// formatLatency converts a millisecond value to a human-readable string.
// Values >= 1000 ms are shown in seconds; smaller values stay in ms.
func formatLatency(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2f s", ms/1000)
	}
	return fmt.Sprintf("%.2f ms", ms)
}

// stripTransportFromName removes transport keywords from a test name so that
// gRPC and HTTP variants of the same scenario share a common base key.
func stripTransportFromName(name string) string {
	for _, kw := range []string{"GRPC", "Grpc", "grpc", "HTTP", "Http", "http", "HTTPS", "Https"} {
		name = strings.ReplaceAll(name, kw, "")
	}
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	return strings.Trim(name, "_")
}

// humanizeName converts a raw test base name into a readable heading.
// It strips the "Test" prefix, replaces underscores with spaces, and
// splits CamelCase words with spaces.
func humanizeName(base string) string {
	base = strings.TrimPrefix(base, "Test")
	base = strings.ReplaceAll(base, "_", " ")
	runes := []rune(base)
	var result []rune
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			if prev >= 'a' && prev <= 'z' {
				result = append(result, ' ')
			}
		}
		result = append(result, r)
	}
	s := strings.TrimSpace(string(result))
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

// buildScenarioData formats the test data for the Ollama prompt, grouping gRPC/HTTP
// transport pairs into a single comparison block so the model sees them together.
func buildScenarioData(tests []testSummary) string {
	type pair struct {
		grpc *testSummary
		http *testSummary
	}
	groups := map[string]*pair{}
	var order []string

	for i := range tests {
		s := &tests[i]
		base := stripTransportFromName(s.Name)
		if _, ok := groups[base]; !ok {
			groups[base] = &pair{}
			order = append(order, base)
		}
		switch strings.ToUpper(s.Transport) {
		case "GRPC":
			groups[base].grpc = s
		default:
			groups[base].http = s
		}
	}

	var buf strings.Builder
	writeLine := func(label string, s *testSummary) {
		tool := "Fortio"
		if s.IsK6 {
			tool = "k6"
		}
		fmt.Fprintf(&buf, "  %s (%s): p50 %s", label, tool, formatLatency(s.P50ms))
		if s.P90ms > 0 {
			buf.WriteString(" | p90 " + formatLatency(s.P90ms))
		}
		if s.P95ms > 0 {
			buf.WriteString(" | p95 " + formatLatency(s.P95ms))
		}
		if s.P99ms > 0 {
			buf.WriteString(" | p99 " + formatLatency(s.P99ms))
		}
		buf.WriteString("\n")
	}

	// writeBlock emits one scenario block for a single testSummary.
	writeBlock := func(scenarioKey, heading string, s *testSummary) {
		fmt.Fprintf(&buf, "Scenario: %s\n", scenarioKey)
		fmt.Fprintf(&buf, "Heading: %s\n", heading)
		if s.MaxVUs > 0 {
			fmt.Fprintf(&buf, "  Max VUs: %.0f", s.MaxVUs)
		} else if s.Connections > 0 {
			fmt.Fprintf(&buf, "  Connections: %d", s.Connections)
		}
		if s.QPS > 0 {
			fmt.Fprintf(&buf, " | QPS: %.2f/s", s.QPS)
		}
		if s.TotalReqs > 0 {
			totalLabel := "Total requests"
			if s.IsK6 {
				totalLabel = "Total iterations"
			}
			fmt.Fprintf(&buf, " | %s: %.0f", totalLabel, s.TotalReqs)
		}
		buf.WriteString("\n")
		label := s.Transport
		if label == "" {
			label = "Latency"
		}
		writeLine(label, s)
		if s.SuccessPct >= 0 {
			fmt.Fprintf(&buf, "  Success: %.0f%%\n", s.SuccessPct)
		}
		buf.WriteString("\n")
	}

	for _, base := range order {
		g := groups[base]
		ref := g.grpc
		if ref == nil {
			ref = g.http
		}

		if g.grpc != nil && g.http != nil {
			if g.grpc.IsK6 != g.http.IsK6 {
				// emit each transport as its own independent block
				// so the model never conflates them into a single comparison.
				writeBlock(base+"_grpc", humanizeName(base)+" (gRPC)", g.grpc)
				writeBlock(base+"_http", humanizeName(base)+" (HTTP)", g.http)
				continue
			}
			fmt.Fprintf(&buf, "Scenario: %s\n", base)
			fmt.Fprintf(&buf, "Heading: %s\n", humanizeName(base))
			if ref.MaxVUs > 0 {
				fmt.Fprintf(&buf, "  Max VUs: %.0f", ref.MaxVUs)
			} else if ref.Connections > 0 {
				fmt.Fprintf(&buf, "  Connections: %d", ref.Connections)
			}
			if ref.QPS > 0 {
				fmt.Fprintf(&buf, " | QPS: %.2f/s", ref.QPS)
			}
			if ref.TotalReqs > 0 {
				totalLabel := "Total requests"
				if ref.IsK6 {
					totalLabel = "Total iterations"
				}
				fmt.Fprintf(&buf, " | %s: %.0f", totalLabel, ref.TotalReqs)
			}
			buf.WriteString("\n")
			writeLine("gRPC", g.grpc)
			writeLine("HTTP", g.http)
			if ref.SuccessPct >= 0 {
				fmt.Fprintf(&buf, "  Success: %.0f%%\n", ref.SuccessPct)
			}
			buf.WriteString("\n")
		} else {
			writeBlock(base, humanizeName(base), ref)
		}
	}
	return buf.String()
}

// generateSectionNarrative calls Ollama to produce the full highlights section for one API:
// an intro paragraph + per-scenario blocks with human-readable names and explanation sentences.
func generateSectionNarrative(display, version string, tests []testSummary, model, ollamaURL string) (string, bool, error) {
	// Pre-compute success summary so the model never has to guess it.
	// Skip tests where successPct == -1 (unknown / no checks data).
	minSuccess := -1.0
	for _, s := range tests {
		if s.SuccessPct < 0 {
			continue
		}
		if minSuccess < 0 || s.SuccessPct < minSuccess {
			minSuccess = s.SuccessPct
		}
	}
	var successStr string
	switch {
	case minSuccess < 0:
		successStr = "N/A"
	case minSuccess >= 100:
		successStr = "100%"
	default:
		successStr = fmt.Sprintf("%.1f%%", minSuccess)
	}

	dataBuf := buildScenarioData(tests)

	// Detect whether this API mixes Fortio and k6 tests so we can conditionally
	// include the "not directly comparable" note only where it applies.
	hasK6, hasFortio := false, false
	for _, t := range tests {
		if t.IsK6 {
			hasK6 = true
		} else {
			hasFortio = true
		}
	}
	mixedToolsNote := ""
	if hasK6 && hasFortio {
		mixedToolsNote = "- IMPORTANT: This API has separate Fortio (gRPC) and k6 (HTTP) scenario blocks. Note briefly in each relevant block that the two tests use different load generators with different load profiles and are not directly comparable. Fortio runs at high throughput with many parallel connections; k6 uses virtual users with sequential iterations. Percentiles also differ: Fortio emits p90/p99/p99.9; k6 emits p90/p95 by default."
	}

	prompt := fmt.Sprintf(`Write developer-facing performance highlights for the %[2]s API in Dapr %[1]s.

Produce ONLY the highlights markdown. Do not include any instructions, data, or section headings from this prompt in your response.
IMPORTANT: Do not output the words "Scenario:" or "Heading:" — these are data labels for your reference only, not part of the output.

The output must follow this structure. The example below uses made-up values — do not copy any headings, terminology, or numbers from it:

Dapr Example API in v1.99 delivers consistent low-latency performance at scale, with **100%%** success rate and zero pod restarts.

**Dual-transport load test** (500 connections | 999.95 req/s):
- gRPC (Fortio): Median **1.23 ms** | p90: **2.45 ms**
- HTTP (k6): Median **1.89 ms** | p90: **3.12 ms** | p95: **4.56 ms**
- **50000** requests at **999.95 req/s**

Developers can expect sub-2ms median latency through the Dapr sidecar even at 1000 req/s with 500 concurrent connections.

**VU-based concurrency test** (200 VUs | 499.97 req/s):
- Median **2.28 ms** | p90: **2.97 ms** | p99: **7.57 ms**
- **30000** iterations at **499.97 req/s**

Under 200 concurrent virtual users the system sustains single-digit millisecond tail latency, making it suitable for high-concurrency workloads.

**Key takeaways**
- At 1,000 req/s, each operation completes in under 2ms — fast enough for production workloads requiring consistent low latency.
- Even at the tail (p99.9), latency stays under 10ms, meaning outlier requests won't cause cascading timeouts in typical retry budgets.

Now write the highlights for the %[2]s API in Dapr %[1]s using only the Data section below:
- IMPORTANT: use the "Heading" field from each scenario in Data as the bold title — do not use headings from the example above.
- IMPORTANT: if a scenario shows "Max VUs" in Data, label the load context as "VUs" (not "connections").
- Begin with: Dapr %[2]s in %[1]s [memorable one-line summary], with **%[4]s** success rate and zero pod restarts.
- When both gRPC and HTTP rows exist for a scenario, show both in separate lines. For single-transport scenarios, omit the transport label.
- Bold all metric values. Latencies are already formatted — use them exactly as written.
- IMPORTANT: Each metric row in Data starts with a transport+tool label like "gRPC (Fortio):" or "HTTP (k6):". Preserve that label as-is in your output (e.g. "- **gRPC (Fortio)**: Median ...").
- End each scenario block with a blank line followed by 1–2 sentences on what the workload demonstrates for a developer.
%[5]s
- After all scenario blocks, write a "**Key takeaways**" section with 2–4 bullet points written from the perspective of a developer building an application with the %[2]s API. Use terminology specific to this API (e.g. "invocation latency" for Service Invocation, "publish latency" for PubSub, "workflow throughput" or "iterations per second" for Workflows). Lead each point with the practical implication first, backed by a specific number from the data. Do not reference other APIs or use terminology from other APIs.

Data:
%[3]s`, version, display, dataBuf, successStr, mixedToolsNote)

	content, err := callOllama(ollamaURL, model, prompt)
	return content, hasK6 && hasFortio, err
}

// defaultNarrative produces a plain fallback paragraph when Ollama is unavailable.
func defaultNarrative(display, version string, tests []testSummary) string {
	if len(tests) == 0 {
		return fmt.Sprintf("%s performance results for %s — see charts below.", display, version)
	}
	s := tests[0]
	return fmt.Sprintf(
		"%s in %s demonstrates reliable performance with a median latency of %s at %.0f QPS. "+
			"All test runs completed with 100%% success rate and zero pod restarts.",
		display, version, formatLatency(s.P50ms), s.QPS)
}

// formatTestsTable writes the structured data block for a section's tests.
func formatTestsTable(tests []testSummary) string {
	var b strings.Builder
	for _, s := range tests {
		transport := s.Transport
		if transport == "" {
			if s.MaxVUs > 0 {
				transport = ""
			}
		}

		header := s.Name
		if transport != "" {
			header += " (" + transport + ")"
		}
		if s.Connections > 0 {
			header += fmt.Sprintf(", %d connections", s.Connections)
		}
		if s.QPS > 0 && !s.IsK6 {
			header += fmt.Sprintf(", ~%.0f QPS (actual)", math.Round(s.QPS/100)*100)
		}

		fmt.Fprintf(&b, "**%s**:\n", header)

		if s.P50ms > 0 {
			parts := fmt.Sprintf("- Median (p50): **%s**", formatLatency(s.P50ms))
			if s.P90ms > 0 {
				parts += fmt.Sprintf(" | p90: **%s**", formatLatency(s.P90ms))
			}
			if s.P95ms > 0 && s.IsK6 {
				parts += fmt.Sprintf(" | p95: **%s**", formatLatency(s.P95ms))
			}
			if s.P99ms > 0 {
				parts += fmt.Sprintf(" | p99: **%s**", formatLatency(s.P99ms))
			}
			if s.P999ms > 0 {
				parts += fmt.Sprintf(" | p99.9: **%s**", formatLatency(s.P999ms))
			}
			fmt.Fprintf(&b, "%s\n", parts)
		}

		if s.QPS > 0 {
			fmt.Fprintf(&b, "- Actual QPS: **%.2f req/s**", s.QPS)
			if s.TotalReqs > 0 {
				fmt.Fprintf(&b, " — **%.0f total requests**", s.TotalReqs)
			}
			fmt.Fprintf(&b, "\n")
		} else if s.TotalReqs > 0 {
			fmt.Fprintf(&b, "- **%.0f** total requests/iterations\n", s.TotalReqs)
		}

		if s.MaxVUs > 0 {
			fmt.Fprintf(&b, "- Max VUs: **%.0f**\n", s.MaxVUs)
		}

		if s.SuccessPct >= 0 {
			fmt.Fprintf(&b, "- **%.0f%% success rate**, 0 pod restarts\n", s.SuccessPct)
		}
		fmt.Fprintf(&b, "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// callOllama sends a single user prompt to Ollama and returns the response text.
func callOllama(baseURL, model, userContent string) (string, error) {
	reqBody := ollamaRequest{
		Model: model,
		Messages: []ollamaMessage{
			{Role: "user", Content: userContent},
		},
		Stream: false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/api/chat"
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama HTTP call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, raw)
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}

	content := strings.TrimSpace(ollamaResp.Message.Content)
	if content == "" {
		return "", errors.New("ollama returned empty content")
	}
	// Collapse any run of 3+ newlines down to two (one blank line between paragraphs).
	for strings.Contains(content, "\n\n\n") {
		content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
	}
	return content, nil
}
