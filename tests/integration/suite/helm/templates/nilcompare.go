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

package templates

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nilcompare))
}

// nilcompare verifies that no chart template compares a value with nil
// through the template eq or ne functions. The Go template engine in the
// Helm release used to install the chart in e2e (v3.3) rejects such a
// comparison with "incompatible types for comparison" as soon as the value
// is set, so a guard like `if ne .Values.x nil` renders only while the
// value is unset and breaks the install the moment it is given. Recent Go
// releases accept the comparison, so rendering with the framework's helm
// helper cannot catch it. Use truthiness (`if .Values.x`) or `kindIs
// "invalid"` instead.
type nilcompare struct{}

func (n *nilcompare) Setup(t *testing.T) []framework.Option {
	return nil
}

func (n *nilcompare) Run(t *testing.T, ctx context.Context) {
	chartDir := filepath.Join(binary.RootDir(t), "charts", "dapr")
	pattern := regexp.MustCompile(`{{[^}]*\b(eq|ne)\b[^}]*\bnil\b[^}]*}}`)

	var offending []string
	err := filepath.WalkDir(chartDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.Contains(path, string(filepath.Separator)+"templates"+string(filepath.Separator)) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(content), "\n") {
			if pattern.MatchString(line) {
				rel, rerr := filepath.Rel(chartDir, path)
				if rerr != nil {
					rel = path
				}
				offending = append(offending, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, offending, "chart templates must not compare values with nil via eq/ne (Helm v3.3 rejects it once the value is set)")
}
