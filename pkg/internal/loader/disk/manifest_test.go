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

package disk

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

// A single YAML document larger than bufio.MaxScanTokenSize makes the scanner
// fail with bufio.ErrTooLong. The scanner keeps returning that error, so the
// loader must give up on the file instead of retrying it forever.
func Test_manifestLargerThanScannerBuffer(t *testing.T) {
	const small = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
  version: v1
`

	large := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: large
spec:
  type: state.couchbase
  version: v1
  metadata:
  - name: blob
    value: %s
`, strings.Repeat("a", bufio.MaxScanTokenSize))

	// returns fails the test, instead of hanging it, when f does not return.
	returns := func(t *testing.T, f func()) {
		t.Helper()

		done := make(chan struct{})
		go func() {
			defer close(done)
			f()
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("loading a manifest larger than the scanner buffer did not return")
		}
	}

	t.Run("decoding reports the error", func(t *testing.T) {
		d := NewComponents(Options{Paths: []string{t.TempDir()}}).(*disk[compapi.Component])
		set := &manifestSet[compapi.Component]{d: d}

		var err error
		returns(t, func() {
			err = set.decodeYaml(strings.NewReader(small + "---\n" + large))
		})

		require.ErrorIs(t, err, bufio.ErrTooLong)
		// The manifest before the oversized one is still loaded.
		require.Len(t, set.ts, 1)
		assert.Equal(t, "statestore", set.ts[0].Name)
	})

	t.Run("an oversized file does not stop other files from loading", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "1-small.yaml"), []byte(small), fs.FileMode(0o600)))
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "2-large.yaml"), []byte(large), fs.FileMode(0o600)))

		var (
			components []compapi.Component
			err        error
		)
		returns(t, func() {
			components, err = NewComponents(Options{Paths: []string{tmp}}).Load(t.Context())
		})

		require.NoError(t, err)
		require.Len(t, components, 1)
		assert.Equal(t, "statestore", components[0].Name)
	})
}
