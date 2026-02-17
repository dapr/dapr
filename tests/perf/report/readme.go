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

// writeFolderReadme writes a README.md in dir that lists all PNGs using their exact filenames.
func writeFolderReadme(dir string, imagePrefix string) error {
	names := listPngFiles(dir)
	if len(names) == 0 {
		return nil
	}
	var b strings.Builder
	for _, name := range names {
		path := name
		if imagePrefix != "" {
			path = imagePrefix + name
		}
		b.WriteString("![")
		b.WriteString(name)
		b.WriteString("](")
		b.WriteString(path)
		b.WriteString(")\n")
	}
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o644)
}

// writeReadmes walks baseOutputDir and writes README.md per folder that has charts, and combined READMEs at API roots that have both http && grpc
func writeReadmes(baseOutputDir string) {
	dirsWithPng := make(map[string]bool)
	_ = filepath.WalkDir(baseOutputDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
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
		if err := os.WriteFile(readmePath, []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o644); err != nil {
			log.Printf("warning: could not write combined README %s: %v", readmePath, err)
		}
	}
}

// appendReadmeContent appends image links for each PNG in dir, using exact filenames.
func appendReadmeContent(b *strings.Builder, dir, imagePrefix string) {
	names := listPngFiles(dir)
	for _, name := range names {
		path := imagePrefix + name
		b.WriteString("![")
		b.WriteString(name)
		b.WriteString("](")
		b.WriteString(path)
		b.WriteString(")\n")
	}
}
