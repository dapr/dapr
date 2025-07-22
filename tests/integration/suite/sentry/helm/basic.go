/*
Copyright 2024 The Dapr Authors
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

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/helm"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	helm *helm.Helm
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=false"), // Not HA
		helm.WithShowOnlySentryDeployment(),
	)

	return []framework.Option{
		framework.WithProcesses(b.helm),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	var dep appsv1.Deployment
	bs, err := io.ReadAll(b.helm.Stdout(t))
	require.NoError(t, err)

	// Split YAML documents by '---' and find the Deployment
	docs := strings.Split(string(bs), "---")
	var deploymentYAML string
	for _, doc := range docs {
		if strings.Contains(doc, "kind: Deployment") {
			deploymentYAML = strings.TrimSpace(doc)
			break
		}
	}
	require.NotEmpty(t, deploymentYAML, "Could not find Deployment in helm output")

	require.NoError(t, yaml.Unmarshal([]byte(deploymentYAML), &dep))
	require.Equal(t, int32(1), *dep.Spec.Replicas)
	require.NotNil(t, dep.Spec.Template.Spec.Affinity)
	require.NotNil(t, dep.Spec.Template.Spec.Affinity.NodeAffinity)

	require.NotEmpty(t, dep.Spec.Template.Spec.Containers)
	require.Equal(t, "dapr-sentry", dep.Spec.Template.Spec.Containers[0].Name)
}
