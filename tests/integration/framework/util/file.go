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

package util

import (
	"path/filepath"
	"strings"
	"testing"

	fuzz "github.com/google/gofuzz"
	"k8s.io/apimachinery/pkg/api/validation/path"
)

func FileNames(t *testing.T, num int) []string {
	if num < 0 {
		t.Fatalf("file name count must be >= 0, got %d", num)
	}

	fz := fuzz.New()

	names := make([]string, num)
	taken := make(map[string]bool)

	forbiddenChars := func(s string) bool {
		for _, c := range forbiddenFileChars {
			if strings.Contains(s, c) {
				return true
			}
		}
		return false
	}

	dir := t.TempDir()
	for i := 0; i < num; i++ {
		var fileName string
		for len(fileName) == 0 ||
			strings.HasPrefix(fileName, "..") ||
			strings.HasPrefix(fileName, " ") ||
			fileName == "." ||
			forbiddenChars(fileName) ||
			len(path.IsValidPathSegmentName(fileName)) > 0 ||
			taken[fileName] {
			fileName = ""
			fz.Fuzz(&fileName)
		}

		taken[fileName] = true
		names[i] = filepath.Join(dir, fileName)
	}

	return names
}
