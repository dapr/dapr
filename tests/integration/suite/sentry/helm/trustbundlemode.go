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

package helm

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(trustbundlemode))
}

// trustbundlemode ensures the trust bundle Secret, which holds the CA issuer
// key, is mounted with restrictive file permissions rather than the 0644
// Kubernetes default.
type trustbundlemode struct {
	helm *helm.Helm
}

func (m *trustbundlemode) Setup(t *testing.T) []framework.Option {
	m.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=false"),
		helm.WithShowOnlySentryDeployment(),
	)

	return []framework.Option{
		framework.WithProcesses(m.helm),
	}
}

func (m *trustbundlemode) Run(t *testing.T, ctx context.Context) {
	bs, err := io.ReadAll(m.helm.Stdout(t))
	require.NoError(t, err)

	var deploymentYAML string
	for doc := range strings.SplitSeq(string(bs), "---") {
		if strings.Contains(doc, "kind: Deployment") {
			deploymentYAML = strings.TrimSpace(doc)
			break
		}
	}
	require.NotEmpty(t, deploymentYAML, "Could not find Deployment in helm output")

	var dep appsv1.Deployment
	require.NoError(t, yaml.Unmarshal([]byte(deploymentYAML), &dep))

	var creds *corev1.Volume
	for i, vol := range dep.Spec.Template.Spec.Volumes {
		if vol.Name == "credentials" {
			creds = &dep.Spec.Template.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, creds, "credentials volume not found")
	require.NotNil(t, creds.Secret)
	require.NotNil(t, creds.Secret.DefaultMode)
	assert.Equal(t, int32(0o440), *creds.Secret.DefaultMode)
}
