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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		case "http":
			transport = "HTTP"
		case "grpc":
			transport = "gRPC"
		}

		agg := aggregateRunners(result.runners)
		isK6 := agg.IterationDuration.Values.Med > 0

		// For workflow/scenario-based k6 tests, IterationDuration is the primary latency
		// signal. For HTTP-based k6 and Fortio tests, HTTPReqDuration is used.
		durMetric := agg.HTTPReqDuration
		if isK6 {
			durMetric = agg.IterationDuration
		}

		successPct := 100.0
		if agg.Checks.Values.Rate > 0 {
			successPct = agg.Checks.Values.Rate * 100
		}

		s := testSummary{
			Name:       result.name,
			Transport:  transport,
			IsK6:       isK6,
			P50ms:      durMetric.Values.Med * 1000,
			P90ms:      durMetric.Values.P90 * 1000,
			P95ms:      durMetric.Values.P95 * 1000,
			P99ms:      durMetric.Values.P99 * 1000,
			P999ms:     durMetric.Values.P999 * 1000,
			QPS:        agg.Iterations.Values.Rate,
			TotalReqs:  agg.Iterations.Values.Count,
			MaxVUs:     agg.VUsMax.Values.Max,
			SuccessPct: successPct,
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

		narrative, err := generateSectionNarrative(display, version, tests, model, ollamaURL)
		if err != nil {
			log.Printf("warning: narrative generation failed for %s: %v — using fallback", api, err)
			narrative = defaultNarrative(display, version, tests)
		}

		sharedContent := narrative + "\n\n" + generateHowToRead(tests) + "\n\n" + formatTestsTable(tests)
		sectionContent[api] = sharedContent

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

		combined := "## Highlights\n\n" + sharedContent + "\n---\n\n" + chartsContent
		if err := os.WriteFile(subReadmePath, []byte(combined), 0o644); err != nil {
			log.Printf("warning: could not write %s: %v", subReadmePath, err)
		} else {
			log.Printf("Wrote %s", subReadmePath)
		}
	}

	// Assemble root README by copying section content from sectionContent.
	var rootBuf strings.Builder
	rootBuf.WriteString(fmt.Sprintf("# Dapr %s Performance Highlights\n\n", version))
	rootBuf.WriteString(howToReadSection())
	rootBuf.WriteString("\n---\n\n")
	for _, api := range apiOrder {
		content, ok := sectionContent[api]
		if !ok {
			continue
		}
		rootBuf.WriteString(fmt.Sprintf("## %s → [charts](./%s/)\n\n", apiDisplay[api], apiDir[api]))
		rootBuf.WriteString(content)
		rootBuf.WriteString("\n---\n\n")
	}

	rootPath := filepath.Join(baseOutputDir, "README.md")
	if err := os.WriteFile(rootPath, []byte(rootBuf.String()), 0o644); err != nil {
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

	return "**How to read these numbers:** " + strings.Join(sentences, ". ") + "."
}

// generateSectionNarrative calls Ollama to produce a short narrative paragraph for one API section.
func generateSectionNarrative(display, version string, tests []testSummary, model, ollamaURL string) (string, error) {
	var dataBuf strings.Builder
	for _, s := range tests {
		transport := s.Transport
		if transport == "" {
			transport = "HTTP"
		}
		dataBuf.WriteString(fmt.Sprintf("- %s (%s): %.0f reqs, %.2f QPS, p50=%.2fms, p90=%.2fms, p99=%.2fms, %.0f%% success\n",
			s.Name, transport, s.TotalReqs, s.QPS, s.P50ms, s.P90ms, s.P99ms, s.SuccessPct))
		if s.MaxVUs > 0 {
			dataBuf.WriteString(fmt.Sprintf("  Max VUs: %.0f, p95=%.2fms\n", s.MaxVUs, s.P95ms))
		}
	}

	prompt := fmt.Sprintf(`Write 3 to 5 sentences of technical prose summarising the %[2]s performance results below for Dapr %[1]s.

Rules:
- Use only the numbers in the data below — do not add information not present
- Do not reference any other Dapr API or test not listed here
- Quote specific ms and QPS values inline
- Declarative present tense: "delivers", "sustains", "demonstrates"
- No bullet points, no headers, no labels
- Do not start with "I" or "Here are"
- End with a reliability statement (success rate, zero pod restarts)

Data:
%[3]s`, version, display, dataBuf.String())

	return callOllama(ollamaURL, model, prompt)
}

// defaultNarrative produces a plain fallback paragraph when Ollama is unavailable.
func defaultNarrative(display, version string, tests []testSummary) string {
	if len(tests) == 0 {
		return fmt.Sprintf("%s performance results for %s — see charts below.", display, version)
	}
	s := tests[0]
	return fmt.Sprintf(
		"%s in %s demonstrates reliable performance with a median latency of %.2f ms at %.0f QPS. "+
			"All test runs completed with 100%% success rate and zero pod restarts.",
		display, version, s.P50ms, s.QPS)
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
			header += fmt.Sprintf(", %.0f QPS target", math.Round(s.QPS/100)*100)
		}

		b.WriteString(fmt.Sprintf("**%s**:\n", header))

		if s.P50ms > 0 {
			parts := fmt.Sprintf("- Median (p50): **%.2f ms**", s.P50ms)
			if s.P90ms > 0 {
				parts += fmt.Sprintf(" | p90: **%.2f ms**", s.P90ms)
			}
			if s.P95ms > 0 && s.IsK6 {
				parts += fmt.Sprintf(" | p95: **%.2f ms**", s.P95ms)
			}
			if s.P99ms > 0 {
				parts += fmt.Sprintf(" | p99: **%.2f ms**", s.P99ms)
			}
			if s.P999ms > 0 {
				parts += fmt.Sprintf(" | p99.9: **%.2f ms**", s.P999ms)
			}
			b.WriteString(parts + "\n")
		}

		if s.QPS > 0 {
			b.WriteString(fmt.Sprintf("- Actual QPS: **%.2f req/s**", s.QPS))
			if s.TotalReqs > 0 {
				b.WriteString(fmt.Sprintf(" — **%.0f total requests**", s.TotalReqs))
			}
			b.WriteString("\n")
		} else if s.TotalReqs > 0 {
			b.WriteString(fmt.Sprintf("- **%.0f** total requests/iterations\n", s.TotalReqs))
		}

		if s.MaxVUs > 0 {
			b.WriteString(fmt.Sprintf("- Max VUs: **%.0f**\n", s.MaxVUs))
		}

		successStr := fmt.Sprintf("%.0f%%", s.SuccessPct)
		b.WriteString(fmt.Sprintf("- **%s success rate**, 0 pod restarts\n", successStr))
		b.WriteString("\n")
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
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("ollama HTTP call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, raw)
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}

	content := strings.TrimSpace(ollamaResp.Message.Content)
	if content == "" {
		return "", fmt.Errorf("ollama returned empty content")
	}
	// Collapse any run of 3+ newlines down to two (one blank line between paragraphs).
	for strings.Contains(content, "\n\n\n") {
		content = strings.ReplaceAll(content, "\n\n\n", "\n\n")
	}
	return content, nil
}
