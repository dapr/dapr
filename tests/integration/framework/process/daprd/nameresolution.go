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

package daprd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
)

// macOS 15+ blocks mDNS multicast for unsigned binaries, so daprds on darwin
// can't discover each other through the default standalone name resolver.
// There, every daprd in the test run resolves app IDs through one shared SQLite
// database instead, which keeps the single global namespace mDNS provides.
// The database outlives any one test, so it is not under a t.TempDir.
var sharedNRDBPath = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "dapr-integration-nr-")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nr.db"), nil
})

func useSQLiteNameResolution() bool {
	return runtime.GOOS == "darwin"
}

func sqliteNameResolutionDB(t *testing.T) string {
	t.Helper()

	path, err := sharedNRDBPath()
	require.NoError(t, err)
	return path
}

// NameResolutionSpec returns the name resolution spec for tests that set one
// explicitly in a Configuration: mDNS, or SQLite on darwin.
func NameResolutionSpec(t *testing.T) *configapi.NameResolutionSpec {
	t.Helper()

	if !useSQLiteNameResolution() {
		return &configapi.NameResolutionSpec{Component: "mdns"}
	}

	b, err := json.Marshal(map[string]string{
		"connectionString": sqliteNameResolutionDB(t),
	})
	require.NoError(t, err)

	return &configapi.NameResolutionSpec{
		Component: "sqlite",
		Configuration: &configapi.DynamicValue{
			JSON: apiextv1.JSON{Raw: b},
		},
	}
}

// NameResolutionJSON returns NameResolutionSpec as JSON, for tests that build
// Configuration manifests as raw strings.
func NameResolutionJSON(t *testing.T) string {
	t.Helper()

	b, err := json.Marshal(NameResolutionSpec(t))
	require.NoError(t, err)
	return string(b)
}

// nameResolutionConfig writes a standalone Configuration selecting the SQLite
// name resolver, or returns "" when the default mDNS resolver works.
func nameResolutionConfig(t *testing.T) string {
	t.Helper()

	if !useSQLiteNameResolution() {
		return ""
	}

	b, err := json.Marshal(map[string]any{
		"apiVersion": "dapr.io/v1alpha1",
		"kind":       "Configuration",
		"metadata":   map[string]string{"name": "nameresolution"},
		"spec": map[string]any{
			"nameResolution": NameResolutionSpec(t),
		},
	})
	require.NoError(t, err)

	f := filepath.Join(t.TempDir(), "nameresolution.yaml")
	require.NoError(t, os.WriteFile(f, b, 0o600))
	return f
}
