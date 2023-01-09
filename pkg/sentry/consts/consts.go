/*
Copyright 2022 The Dapr Authors
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

package consts

const (
	// ServiceAccountTokenAudience is the default audience that Sentry looks for in tokens to validate pods.
	ServiceAccountTokenAudience = "dapr.io/sentry" /* #nosec */
	// TrustBundleK8sSecretName is the name of the kubernetes secret that holds the trust bundle.
	TrustBundleK8sSecretName = "dapr-trust-bundle" /* #nosec */
	// ClientBundleK8sSecretName is the name of the kubernetes secret that holds the client trust bundle.
	ClientBundleK8sSecretName = "dapr-client-bundle" /* #nosec */
	// TrustAnchorsEnvVar is the environment variable name for the trust anchors in the sidecar.
	TrustAnchorsEnvVar = "DAPR_TRUST_ANCHORS"
	CertChainEnvVar    = "DAPR_CERT_CHAIN"
	CertKeyEnvVar      = "DAPR_CERT_KEY"
)
