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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Declarative subscriptions on the same topic are resolved by load order (the
// first one declared wins), so the loader must return them in the order they
// are declared: by directory, then by file, then by position in the file.
func Test_subscriptionsLoadOrder(t *testing.T) {
	v1 := func(name string) string {
		return `apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: ` + name + `
spec:
  pubsubname: pubsub
  topic: orders
  route: /` + name + `
`
	}
	v2 := func(name string) string {
		return `apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: ` + name + `
spec:
  pubsubname: pubsub
  topic: orders
  routes:
    default: /` + name + `
`
	}
	write := func(t *testing.T, dir, file string, docs ...string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(strings.Join(docs, "---\n")), fs.FileMode(0o600)))
	}
	names := func(t *testing.T, dirs ...string) []string {
		t.Helper()
		subs, err := NewSubscriptions(Options{Paths: dirs, AppID: "myappid"}).Load(t.Context())
		require.NoError(t, err)
		out := make([]string, len(subs))
		for i, sub := range subs {
			out[i] = sub.Name
		}
		return out
	}

	t.Run("a file with several subscriptions, followed by another file", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "a.yaml", v2("a1"), v2("a2"))
		write(t, dir, "b.yaml", v2("b1"))

		assert.Equal(t, []string{"a1", "a2", "b1"}, names(t, dir))
	})

	t.Run("several files in one directory, v1alpha1 and v2alpha1 mixed", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "a.yaml", v2("a1"), v1("a2"), v2("a3"))
		write(t, dir, "b.yaml", v2("b1"), v1("b2"))
		write(t, dir, "c.yaml", v1("c1"))

		assert.Equal(t, []string{"a1", "a2", "a3", "b1", "b2", "c1"}, names(t, dir))
	})

	t.Run("several directories", func(t *testing.T) {
		dir1, dir2 := t.TempDir(), t.TempDir()
		write(t, dir1, "a.yaml", v2("a1"))
		write(t, dir1, "b.yaml", v2("b1"))
		write(t, dir2, "c.yaml", v1("c1"), v2("c2"))
		write(t, dir2, "d.yaml", v2("d1"))

		assert.Equal(t, []string{"a1", "b1", "c1", "c2", "d1"}, names(t, dir1, dir2))
	})
}
