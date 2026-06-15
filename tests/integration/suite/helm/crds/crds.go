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

package crds

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(uptodate))
}

type uptodate struct{}

func (u *uptodate) Setup(t *testing.T) []framework.Option {
	return nil
}

func (u *uptodate) Run(t *testing.T, ctx context.Context) {
	rootDir := binary.RootDir(t)
	chartDir := filepath.Join(rootDir, "charts", "dapr", "crds")

	genDir := t.TempDir()
	args := []string{
		"crd:crdVersions=v1",
		"paths=./pkg/apis/...",
		"output:crd:artifacts:config=" + genDir,
	}
	//nolint:gosec
	cmd := exec.CommandContext(ctx, binary.EnvValue("controllergen"), args...)
	cmd.Dir = rootDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoErrorf(t, cmd.Run(), "controller-gen failed: %s", stderr.String())

	want := loadCRDs(t, genDir)
	got := loadCRDs(t, chartDir)

	assert.ElementsMatch(t, keys(want), keys(got),
		"chart CRDs (%v) do not match the set of generated resources (%v); run `make code-generate` and copy config/crd/bases/* into charts/dapr/crds",
		keys(got), keys(want))

	for name, wantCRD := range want {
		gotCRD, ok := got[name]
		if !ok {
			continue
		}

		wantSpec := wantCRD.Spec
		gotSpec := gotCRD.Spec
		wantSpec.Conversion = nil
		gotSpec.Conversion = nil

		assert.Equal(t, wantSpec, gotSpec,
			"chart CRD %q is out of date with pkg/apis; run `make code-generate` and copy config/crd/bases/* into charts/dapr/crds", name)
	}
}

func loadCRDs(t *testing.T, dir string) map[string]apiextv1.CustomResourceDefinition {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	crds := make(map[string]apiextv1.CustomResourceDefinition)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ext := filepath.Ext(entry.Name()); ext != ".yaml" && ext != ".yml" {
			continue
		}

		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)

		for doc := range bytes.SplitSeq(b, []byte("\n---")) {
			if len(bytes.TrimSpace(doc)) == 0 {
				continue
			}
			var crd apiextv1.CustomResourceDefinition
			require.NoError(t, yaml.Unmarshal(doc, &crd))
			if crd.Kind != "CustomResourceDefinition" {
				continue
			}
			require.NotEmptyf(t, crd.Name, "CRD in %s has no metadata.name", entry.Name())
			crds[crd.Name] = crd
		}
	}

	return crds
}

func keys(m map[string]apiextv1.CustomResourceDefinition) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
