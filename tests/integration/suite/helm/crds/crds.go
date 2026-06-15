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

	// Generate the CRDs from the API types, mirroring the `make code-generate`
	// invocation in tools/codegen.mk. Output goes to stdout rather than a
	// directory on purpose: controller-gen parses its arguments as markers and
	// splits on ':', so an absolute output path on Windows (e.g. C:\...) is
	// misinterpreted as marker syntax and fails to parse.
	args := []string{
		"crd:crdVersions=v1",
		"paths=./pkg/apis/...",
		"output:stdout",
	}
	//nolint:gosec
	cmd := exec.CommandContext(ctx, binary.EnvValue("controllergen"), args...)
	cmd.Dir = rootDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoErrorf(t, cmd.Run(), "controller-gen failed: %s", stderr.String())

	want := parseCRDs(t, stdout.Bytes(), "controller-gen output")
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

	// The conversion webhook is excluded from the spec comparison above because
	// controller-gen does not emit it; it is maintained by hand in the chart.
	// Assert it explicitly so accidental removal or drift of the subscriptions
	// v1alpha1 <-> v2alpha1 conversion webhook is still caught.
	assertSubscriptionConversion(t, got)
}

func assertSubscriptionConversion(t *testing.T, got map[string]apiextv1.CustomResourceDefinition) {
	t.Helper()

	sub, ok := got["subscriptions.dapr.io"]
	require.True(t, ok, "subscriptions.dapr.io CRD missing from chart")

	conv := sub.Spec.Conversion
	require.NotNil(t, conv, "subscriptions.dapr.io must declare a conversion webhook")
	assert.Equal(t, apiextv1.WebhookConverter, conv.Strategy)
	require.NotNil(t, conv.Webhook)
	require.NotNil(t, conv.Webhook.ClientConfig)
	require.NotNil(t, conv.Webhook.ClientConfig.Service)
	assert.Equal(t, "dapr-webhook", conv.Webhook.ClientConfig.Service.Name)
	require.NotNil(t, conv.Webhook.ClientConfig.Service.Path)
	assert.Equal(t, "/convert", *conv.Webhook.ClientConfig.Service.Path)
	assert.ElementsMatch(t, []string{"v1", "v2alpha1"}, conv.Webhook.ConversionReviewVersions)
}

// loadCRDs reads every YAML file in dir and returns the CustomResourceDefinitions
// they contain, keyed by metadata.name.
func loadCRDs(t *testing.T, dir string) map[string]apiextv1.CustomResourceDefinition {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var buf bytes.Buffer
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ext := filepath.Ext(entry.Name()); ext != ".yaml" && ext != ".yml" {
			continue
		}

		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		buf.Write(b)
		buf.WriteString("\n---\n")
	}

	return parseCRDs(t, buf.Bytes(), dir)
}

// parseCRDs splits a (possibly multi-document) YAML stream into the
// CustomResourceDefinitions it contains, keyed by metadata.name. It fails if the
// same name appears more than once so that "exactly one CRD per resource" is
// actually enforced. source is used only for error messages.
func parseCRDs(t *testing.T, data []byte, source string) map[string]apiextv1.CustomResourceDefinition {
	t.Helper()

	crds := make(map[string]apiextv1.CustomResourceDefinition)
	for doc := range bytes.SplitSeq(data, []byte("\n---")) {
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		var crd apiextv1.CustomResourceDefinition
		require.NoError(t, yaml.Unmarshal(doc, &crd))
		if crd.Kind != "CustomResourceDefinition" {
			continue
		}
		require.NotEmptyf(t, crd.Name, "CRD in %s has no metadata.name", source)
		_, dup := crds[crd.Name]
		require.Falsef(t, dup, "duplicate CRD %q found in %s", crd.Name, source)
		crds[crd.Name] = crd
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
