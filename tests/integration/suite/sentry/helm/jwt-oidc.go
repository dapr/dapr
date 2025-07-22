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
	suite.Register(new(oidc))
}

type oidc struct {
	helm *helm.Helm
}

func (o *oidc) Setup(t *testing.T) []framework.Option {
	o.helm = helm.New(t,
		helm.WithGlobalValues("ha.enabled=false"), // Not HA
		helm.WithValues(
			"dapr_sentry.jwt.enabled=true",
			"dapr_sentry.jwt.keyFilename=/tmp/jwt.key",
			"dapr_sentry.jwt.jwksFilename=/tmp/jwt.jwks",
			"dapr_sentry.jwt.issuer=dapr.io",
			"dapr_sentry.jwt.signingAlgorithm=RS256",
			"dapr_sentry.jwt.keyID=jwt-key-id",
			"dapr_sentry.oidc.enabled=true",
			"dapr_sentry.oidc.server.port=8080",
			"dapr_sentry.oidc.server.address=0.0.0.0",
			"dapr_sentry.oidc.tls.enabled=true",
			"dapr_sentry.oidc.tls.certFile=/tmp/oidc.crt",
			"dapr_sentry.oidc.tls.keyFile=/tmp/oidc.key",
			"dapr_sentry.oidc.jwksURI=/v1/jwks",
			"dapr_sentry.oidc.pathPrefix=/v1",
			"dapr_sentry.oidc.allowedHosts[0]=dapr.io",
		),
		helm.WithShowOnlySentryDeployment(),
	)

	return []framework.Option{
		framework.WithProcesses(o.helm),
	}
}

func (o *oidc) Run(t *testing.T, ctx context.Context) {
	var dep appsv1.Deployment
	bs, err := io.ReadAll(o.helm.Stdout(t))
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
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--jwt-enabled=true")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--jwt-key-filename=/tmp/jwt.key")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--jwks-filename=/tmp/jwt.jwks")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--jwt-issuer=dapr.io")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--jwt-signing-algorithm=RS256")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--jwt-key-id=jwt-key-id")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--oidc-enabled=true")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--oidc-server-tls-enabled=true")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--oidc-server-tls-cert-file=/tmp/oidc.crt")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--oidc-server-tls-key-file=/tmp/oidc.key")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--oidc-jwks-uri=/v1/jwks")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--oidc-path-prefix=/v1")
	require.Contains(t, dep.Spec.Template.Spec.Containers[0].Args, "--oidc-allowed-hosts=dapr.io")
	found := false
	for _, port := range dep.Spec.Template.Spec.Containers[0].Ports {
		if port.Name == "oidc" {
			found = true
		}
	}
	require.True(t, found, "oidc port should be present in the deployment")
}
