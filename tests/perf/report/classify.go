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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// buildTestModes scans the tests/perf files to determine which tests use k6 vs fortio
// map is testName (func name) corresponding to k6 or fortio as vals.
func buildTestModes(root string) map[string]string {
	result := map[string]string{}
	skipDir := func(p string, d fs.DirEntry) bool {
		if !d.IsDir() {
			return false
		}
		base := filepath.Base(p)
		if base == "report" || base == "utils" || base == "dist" || base == "charts" || base == "logs" {
			return true
		}
		if strings.Contains(p, string(filepath.Separator)+"report"+string(filepath.Separator)) ||
			strings.Contains(p, string(filepath.Separator)+"dist"+string(filepath.Separator)) ||
			strings.Contains(p, string(filepath.Separator)+"logs"+string(filepath.Separator)) {
			return true
		}
		return false
	}
	if walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skipDir(path, d) {
			return fs.SkipDir
		}
		// look for test files
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Dir(path) == filepath.Clean(root) {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(strings.ToLower(base), "readme") ||
			strings.HasPrefix(strings.ToLower(base), "test_params") ||
			strings.HasPrefix(strings.ToLower(base), "test_result") {
			return nil
		}
		var b []byte
		b, err = os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		var mode string
		if strings.Contains(src, "NewK6(") || strings.Contains(src, ".NewK6(") {
			mode = "k6"
		} else if strings.Contains(src, "Params(") || strings.Contains(src, "WithQPS(") {
			mode = "fortio"
		}
		if mode == "" {
			return nil
		}

		// map func name to mode
		for _, line := range strings.Split(src, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "func Test") && strings.Contains(l, "(t *testing.T)") {
				name := l[len("func "):]
				if idx := strings.Index(name, "("); idx != -1 {
					name = strings.TrimSpace(name[:idx])
				}
				if name != "" {
					result[name] = mode
				}
			}
		}
		return nil
	}); walkErr != nil {
		// even if there is an err return what we got
		if debugEnabled {
			debugf("Error walking %s: %s\n", root, walkErr)
		}
		return result
	}
	return result
}

// classifyAPIAndTransport returns api/transport/ok derived from the pkg path
func classifyAPIAndTransport(pkg, test string) (string, string, bool) {
	pkgLower := strings.ToLower(pkg)
	testLower := strings.ToLower(test)
	seg := strings.ToLower(filepath.Base(pkg))

	// Workflows
	if strings.Contains(pkgLower, "/workflows") || strings.Contains(pkgLower, "/workflow") ||
		strings.Contains(seg, "workflow") || seg == "workflows" {
		return "workflows", "", true
	}

	// Service invocation
	if strings.Contains(pkgLower, "/service_invocation") || strings.HasPrefix(seg, "service_invocation") {
		return "service_invocation", classifyTransport(pkgLower, testLower, seg), true
	}

	// Actors
	if strings.Contains(pkgLower, "/actor") || strings.Contains(pkgLower, "/actors") ||
		strings.HasPrefix(seg, "actor") || strings.Contains(seg, "actors") {
		return "actors", classifyTransport(pkgLower, testLower, seg), true
	}

	// State only has grpc so leave in state dir and dont split by transport
	if strings.Contains(pkgLower, "/state") || strings.HasPrefix(seg, "state") {
		return "state", classifyTransport(pkgLower, testLower, seg), true
	}

	// PubSub
	if strings.Contains(pkgLower, "/pubsub") || strings.HasPrefix(seg, "pubsub") {
		transport := classifyTransport(pkgLower, testLower, seg)
		// Bulk pubsub (split into subfolder)
		if strings.Contains(pkgLower, "/pubsub_bulk") || strings.HasPrefix(seg, "pubsub_bulk") ||
			strings.Contains(pkgLower, "bulk_publish") || strings.Contains(seg, "bulk") {
			return "pubsub/bulk", transport, true
		}
		return "pubsub", transport, true
	}

	// Configuration
	if strings.Contains(pkgLower, "/configuration") || seg == "configuration" ||
		strings.HasPrefix(seg, "configuration_") {
		return "configuration", classifyTransport(pkgLower, testLower, seg), true
	}

	// add additional APIs here as we add perf tests

	// for future use as we expand tests
	// chart any other tests under tests/perf/* using the package base name
	if strings.Contains(pkgLower, "/tests/perf") {
		return seg, classifyTransport(pkgLower, testLower, seg), true
	}

	return "", "", false
}

func classifyTransport(pkg, test, seg string) string {
	if strings.Contains(pkg, "http") || strings.Contains(seg, "http") {
		return "http"
	}
	if strings.Contains(pkg, "grpc") || strings.Contains(seg, "grpc") {
		return "grpc"
	}
	if strings.Contains(test, "http") {
		return "http"
	}
	if strings.Contains(test, "grpc") {
		return "grpc"
	}
	return ""
}
