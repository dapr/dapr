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
	"html"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listPngFiles returns sorted PNG filenames in dir
func listPngFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".png") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// extractTestBaseName returns the base test name for header grouping (ex: TestActorActivate)
func extractTestBaseName(filename string) string {
	s := strings.TrimSuffix(filename, ".png")
	// strip known chart type suffixes
	chartSuffixes := []string{
		"_duration_requested_vs_actual", "_duration_breakdown", "_duration_low", "_duration_comparison",
		"_histogram_count", "_histogram_percent",
		"_resource_cpu", "_resource_memory", "_data_volume", "_summary", "_tail_latency", "_throughput",
		"_header_size", "_payload_size", "_qps", "_connection_stats",
	}
	for _, suffix := range chartSuffixes {
		if strings.HasSuffix(s, suffix) {
			s = strings.TrimSuffix(s, suffix)
			break
		}
	}
	// strip `_avg` or `_T_xxx` (ex: iterations, #01) to get base test name (needed for wf charts)
	if i := strings.Index(s, "_avg"); i != -1 {
		return s[:i]
	}
	if i := strings.Index(s, "_T_"); i != -1 {
		return s[:i]
	}
	return s
}

// writeFolderReadme writes a README.md in the dir with test name headers & pngs grouped.
// Use HTML img tags so filenames (underscores, .png, #) render correctly bc they did not
// render properly with the markdown syntax for images
func writeFolderReadme(dir string, imagePrefix string) error {
	names := listPngFiles(dir)
	if len(names) == 0 {
		return nil
	}
	groups := groupPngsByTest(names)
	var b strings.Builder
	for _, group := range groups {
		b.WriteString("### ")
		b.WriteString(group.baseName)
		b.WriteString("\n\n")
		for _, name := range group.files {
			path := name
			if imagePrefix != "" {
				path = imagePrefix + name
			}
			writeImgTag(&b, path, name)
		}
		b.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o600)
}

type testGroup struct {
	baseName string
	files    []string
}

func groupPngsByTest(names []string) []testGroup {
	m := make(map[string][]string)
	var order []string
	for _, name := range names {
		base := extractTestBaseName(name)
		if m[base] == nil {
			order = append(order, base)
		}
		m[base] = append(m[base], name)
	}
	out := make([]testGroup, 0, len(order))
	for _, base := range order {
		out = append(out, testGroup{baseName: base, files: m[base]})
	}
	return out
}

// writeImgTag writes an HTML img tag so the filename (underscores, .png, #) displays & renders correctly
func writeImgTag(b *strings.Builder, path, alt string) {
	b.WriteString("<img src=\"")
	b.WriteString(path)
	b.WriteString("\" alt=\"")
	b.WriteString(html.EscapeString(alt))
	b.WriteString("\" />\n")
}

// writeReadmes walks baseOutputDir and writes README.md per folder that has charts, and combined READMEs at API roots that have both http && grpc
func writeReadmes(baseOutputDir string) {
	dirsWithPng := make(map[string]bool)
	_ = filepath.WalkDir(baseOutputDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if len(listPngFiles(path)) > 0 {
			dirsWithPng[path] = true
		}
		return nil
	})

	// Write per-folder README for each dir that has charts
	for dir := range dirsWithPng {
		if err := writeFolderReadme(dir, ""); err != nil {
			log.Printf("warning: could not write README in %s: %v", dir, err)
		}
	}

	// For dirs that have both http and grpc subdirs with charts, write combined README at parent
	seenParent := make(map[string]bool)
	for dir := range dirsWithPng {
		parent := filepath.Dir(dir)
		base := filepath.Base(dir)
		if base != "http" && base != "grpc" {
			continue
		}
		if seenParent[parent] {
			continue
		}
		httpDir := filepath.Join(parent, "http")
		grpcDir := filepath.Join(parent, "grpc")
		hasHTTP := dirsWithPng[httpDir]
		hasGRPC := dirsWithPng[grpcDir]
		if !hasHTTP && !hasGRPC {
			continue
		}
		seenParent[parent] = true

		var b strings.Builder
		if hasHTTP {
			b.WriteString("## HTTP\n\n")
			appendReadmeContent(&b, httpDir, "http/")
		}
		if hasHTTP && hasGRPC {
			b.WriteString("---------------\n\n")
		}
		if hasGRPC {
			b.WriteString("## gRPC\n\n")
			appendReadmeContent(&b, grpcDir, "grpc/")
		}
		// Pubsub exception: also include bulk/http and bulk/grpc when present
		bulkHTTP := filepath.Join(parent, "bulk", "http")
		bulkGRPC := filepath.Join(parent, "bulk", "grpc")
		hasBulkHTTP := dirsWithPng[bulkHTTP]
		hasBulkGRPC := dirsWithPng[bulkGRPC]
		if hasBulkHTTP || hasBulkGRPC {
			b.WriteString("---------------\n\n")
			b.WriteString("## Bulk\n\n")
			if hasBulkHTTP {
				b.WriteString("### HTTP\n\n")
				appendReadmeContent(&b, bulkHTTP, "bulk/http/")
			}
			if hasBulkHTTP && hasBulkGRPC {
				b.WriteString("\n")
			}
			if hasBulkGRPC {
				b.WriteString("### gRPC\n\n")
				appendReadmeContent(&b, bulkGRPC, "bulk/grpc/")
			}
		}

		readmePath := filepath.Join(parent, "README.md")
		if err := os.WriteFile(readmePath, []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o600); err != nil {
			log.Printf("warning: could not write combined README %s: %v", readmePath, err)
		}
	}
}

// appendReadmeContent appends image links for each PNG in the dir, grouped by test with headers
func appendReadmeContent(b *strings.Builder, dir, imagePrefix string) {
	names := listPngFiles(dir)
	groups := groupPngsByTest(names)
	for _, group := range groups {
		b.WriteString("### ")
		b.WriteString(group.baseName)
		b.WriteString("\n\n")
		for _, name := range group.files {
			path := imagePrefix + name
			writeImgTag(b, path, name)
		}
		b.WriteString("\n")
	}
}
