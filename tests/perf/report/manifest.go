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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// manifest.go emits a small JSON description of a perf run that the docs site
// consumes at build time. The docs page never hard-codes a version: it reads
// the Dapr minor version from its own site config, fetches the matching
// manifest from the perf-charts branch, and renders the tables and charts from
// it. New releases only push a new manifest + charts; no docs change is needed.

var manifestOut = flag.String("manifest", "", "if set, write the perf-results manifest JSON (consumed by the docs site) to this path")

// apiDocMeta gives each chart API folder a human title and ordering weight in
// the manifest. APIs without an entry fall back to the folder name and sort last.
var apiDocMeta = map[string]struct {
	title  string
	weight int
}{
	"service_invocation": {"Service invocation", 10},
	"state":              {"State management", 20},
	"pubsub":             {"Publish and subscribe", 30},
	"actors":             {"Actors", 40},
	"workflows":          {"Workflows", 50},
	"jobs":               {"Jobs", 60},
	"configuration":      {"Configuration", 70},
}

func apiMeta(api string) (string, int) {
	if m, ok := apiDocMeta[api]; ok {
		return m.title, m.weight
	}
	return api, 100
}

var minorRe = regexp.MustCompile(`^(v[0-9]+\.[0-9]+)\.`)

// minorVersion reduces a release tag to its minor series (v1.18.0 -> v1.18),
// which is how the docs site is versioned and keys the perf data. Non release
// values (e.g. "master") are returned unchanged.
func minorVersion(v string) string {
	if m := minorRe.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return v
}

// perfManifest is the document the docs site reads to render the perf pages.
type perfManifest struct {
	Version string    `json:"version"` // full tag, for display, e.g. v1.18.0
	Minor   string    `json:"minor"`   // minor series and chart path root, e.g. v1.18
	Infra   string    `json:"infra"`
	APIs    []perfAPI `json:"apis"`
}

type perfAPI struct {
	Key      string        `json:"key"`
	Title    string        `json:"title"`
	Sections []perfSection `json:"sections"`
}

// perfSection is one renderable block: a protocol variant of an API. Path is
// relative to the version root on the perf-charts branch, so the docs build the
// image URL as <base>/<minor>/<path>/<chart>.
type perfSection struct {
	Label      string         `json:"label,omitempty"` // "", "HTTP", "gRPC", "Bulk / HTTP"
	Path       string         `json:"path"`
	Efficiency []perfEff      `json:"efficiency,omitempty"`
	Scenarios  []perfScenario `json:"scenarios"`
}

type perfEff struct {
	Test         string  `json:"test"`
	ItPerSec     float64 `json:"itPerSec"`
	AppCPUm      float64 `json:"appCPUm"`
	AppMemMB     float64 `json:"appMemMB"`
	SidecarCPUm  float64 `json:"sidecarCPUm"`
	SidecarMemMB float64 `json:"sidecarMemMB"`
	IterPerCore  string  `json:"iterPerCore"`
	IterPerGB    string  `json:"iterPerGB"`
}

type perfScenario struct {
	Name   string      `json:"name"`
	Charts []perfChart `json:"charts"`
}

// perfChart carries the raw filename (for alt text) and a URL-path-escaped
// form so the docs can build the image src without a path-escape helper of
// their own (filenames contain spaces, parentheses and '#').
type perfChart struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// generateManifest walks the rendered chart tree and writes the manifest JSON.
func generateManifest(baseOutputDir string) {
	m := perfManifest{
		Version: *version,
		Minor:   minorVersion(*version),
		Infra:   *infra,
	}

	for _, api := range topLevelAPIs(baseOutputDir) {
		title, _ := apiMeta(api)
		pa := perfAPI{Key: api, Title: title}
		apiDir := filepath.Join(baseOutputDir, api)

		// Charts live either directly in the API folder (workflows, actors,
		// jobs) or split under protocol subfolders.
		if s, ok := buildSection(baseOutputDir, apiDir, ""); ok {
			pa.Sections = append(pa.Sections, s)
		}
		for _, sub := range []struct{ dir, label string }{
			{filepath.Join(apiDir, "http"), "HTTP"},
			{filepath.Join(apiDir, "grpc"), "gRPC"},
			{filepath.Join(apiDir, "bulk", "http"), "Bulk / HTTP"},
			{filepath.Join(apiDir, "bulk", "grpc"), "Bulk / gRPC"},
		} {
			if s, ok := buildSection(baseOutputDir, sub.dir, sub.label); ok {
				pa.Sections = append(pa.Sections, s)
			}
		}

		if len(pa.Sections) > 0 {
			m.APIs = append(m.APIs, pa)
		}
	}

	// -manifest is only set by the publish workflow, so a failure here means CI
	// would otherwise push a missing or partial manifest while the step stays
	// green. Fail fast instead.
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		log.Fatalf("could not marshal perf manifest: %v", err)
	}
	if err := os.WriteFile(*manifestOut, append(data, '\n'), 0o600); err != nil {
		log.Fatalf("could not write perf manifest %s: %v", *manifestOut, err)
	}
	log.Printf("Generated perf manifest for %s at %s\n", *version, *manifestOut)
}

// buildSection assembles the efficiency rows and scenarios for one chart folder.
func buildSection(baseOutputDir, dir, label string) (perfSection, bool) {
	names := listPngFiles(dir)
	if len(names) == 0 {
		return perfSection{}, false
	}
	rel := filepath.ToSlash(strings.TrimPrefix(dir, baseOutputDir+string(os.PathSeparator)))
	sec := perfSection{Label: label, Path: rel}

	rows := efficiencyByDir[dir]
	sort.Slice(rows, func(i, j int) bool { return rows[i].test < rows[j].test })
	for _, r := range rows {
		sec.Efficiency = append(sec.Efficiency, toPerfEff(r))
	}

	for _, g := range groupPngsByTest(names) {
		sc := perfScenario{Name: g.baseName}
		for _, f := range g.files {
			sc.Charts = append(sc.Charts, perfChart{Name: f, File: url.PathEscape(f)})
		}
		sec.Scenarios = append(sec.Scenarios, sc)
	}
	return sec, true
}

// toPerfEff mirrors writeEfficiencyTable's derived columns so the docs render
// identical figures without re-deriving them.
func toPerfEff(r efficiencyRow) perfEff {
	totalCores := (r.appCPUm + r.sidecarCPUm) / 1000
	totalGB := (r.appMemMB + r.sidecarMemMB) / 1024
	perCore := "n/a"
	if totalCores > 0 {
		perCore = fmt.Sprintf("%.1f", r.itPerSec/totalCores)
	}
	perGB := "n/a"
	if totalGB > 0 {
		perGB = fmt.Sprintf("%.1f", r.itPerSec/totalGB)
	}
	return perfEff{
		Test:         r.test,
		ItPerSec:     r.itPerSec,
		AppCPUm:      r.appCPUm,
		AppMemMB:     r.appMemMB,
		SidecarCPUm:  r.sidecarCPUm,
		SidecarMemMB: r.sidecarMemMB,
		IterPerCore:  perCore,
		IterPerGB:    perGB,
	}
}

// topLevelAPIs returns API folders under charts/<version> that contain charts,
// sorted by their docs weight.
func topLevelAPIs(baseOutputDir string) []string {
	entries, err := os.ReadDir(baseOutputDir)
	if err != nil {
		return nil
	}
	var apis []string
	for _, e := range entries {
		if e.IsDir() && dirHasCharts(filepath.Join(baseOutputDir, e.Name())) {
			apis = append(apis, e.Name())
		}
	}
	sort.Slice(apis, func(i, j int) bool {
		_, wi := apiMeta(apis[i])
		_, wj := apiMeta(apis[j])
		if wi != wj {
			return wi < wj
		}
		return apis[i] < apis[j]
	})
	return apis
}

func dirHasCharts(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".png") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
