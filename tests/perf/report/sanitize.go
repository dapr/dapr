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

import "strings"

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

// sanitizePerfJSON removes problematic fields and line-breaks, specifically it:
// Drops the "Payload": "..." field and base64-only continuation lines from the
// following test: tests/perf/service_invocation_http/service_invocation_http_test.go
// & fixes trailing comma issues
func sanitizePerfJSON(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	// when we hit "Payload": ..., skip everything until we reach the next
	// well-known field "LogErrors": ... then resume
	skipping := false
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if skipping {
			if strings.Contains(l, "\"LogErrors\"") {
				skipping = false
				out = append(out, line)
			}
			continue
		}
		if strings.HasPrefix(l, "\"Payload\"") || strings.Contains(l, "\"Payload\":") {
			skipping = true
			continue
		}
		out = append(out, line)
	}
	s = strings.Join(out, "\n")
	// rm trailing commas before ] & }
	s = strings.ReplaceAll(s, ",]", "]")
	s = strings.ReplaceAll(s, ",}", "}")
	// truncate trailing chars after the last closing brace
	if idx := strings.LastIndex(s, "}"); idx != -1 {
		s = s[:idx+1]
	}
	return s
}

// add missing closing braces/brackets if we detect more openings than closings.
// fix for logs where the JSON object may be truncated by line buffering
func repairJSONClosers(s string) string {
	openCurly := strings.Count(s, "{")
	closeCurly := strings.Count(s, "}")
	openSquare := strings.Count(s, "[")
	closeSquare := strings.Count(s, "]")
	var b strings.Builder
	b.WriteString(s)
	// Close arrays first, then objects since most blocks are objects containing arrays
	for range make([]struct{}, openSquare-closeSquare) {
		b.WriteString("]")
	}
	for range make([]struct{}, openCurly-closeCurly) {
		b.WriteString("}")
	}
	return b.String()
}

// rm per-run ordinal nums from test names. ex:
// " ... :_#02" or " ... #02", so repeated runs aggregate under one base key
func stripRunOrdinalSuffix(name string) string {
	candidates := []string{":_#", ":#", " #"} // mostly for wf
	cut := -1
	for _, p := range candidates {
		if i := strings.Index(name, p); i != -1 {
			if cut == -1 || i < cut {
				cut = i
			}
		}
	}
	if cut != -1 {
		return strings.TrimSpace(name[:cut])
	}
	return name
}
