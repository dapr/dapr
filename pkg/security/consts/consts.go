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
	// APITokenEnvVar is the environment variable for the API token.
	//nolint:gosec
	APITokenEnvVar = "DAPR_API_TOKEN"
	// AppAPITokenEnvVar is the environment variable for the app API token.
	//nolint:gosec
	AppAPITokenEnvVar = "APP_API_TOKEN"
	// APITokenHeader is header name for HTTP/gRPC calls to hold the token.
	//nolint:gosec
	APITokenHeader = "dapr-api-token"

	// TrustBundleK8sSecretName is the name of the kubernetes secret that holds the trust bundle.
	TrustBundleK8sSecretName = "dapr-trust-bundle" /* #nosec */

	// TrustAnchorsEnvVar is the environment variable name for the trust anchors in the sidecar.
	TrustAnchorsEnvVar = "DAPR_TRUST_ANCHORS"
	// CertChainEnvVar is the environment variable name for the cert chain in the sidecar.
	CertChainEnvVar = "DAPR_CERT_CHAIN"
	// CertKeyEnvVar is the environment variable name for the cert key in the sidecar.
	CertKeyEnvVar = "DAPR_CERT_KEY"

	// EnvKeysEnvVar is the variable injected in the daprd container with the list of injected env vars.
	EnvKeysEnvVar = "DAPR_ENV_KEYS"

	// SentryLocalIdentityEnvVar is the environment variable for the local identity sent to Sentry.
	SentryLocalIdentityEnvVar = "SENTRY_LOCAL_IDENTITY"
	// SentryTokenFileEnvVar is the environment variable for the Sentry token file.
	//nolint:gosec
	SentryTokenFileEnvVar = "DAPR_SENTRY_TOKEN_FILE"

	// AnnotationKeyControlPlane is the annotation to mark a control plane
	// component. The value is the name of the control plane service.
	AnnotationKeyControlPlane = "dapr.io/control-plane"

	// ControlPlaneAddressEnvVar is the daprd environment variable for
	// configuring the control plane namespace.
	ControlPlaneNamespaceEnvVar = "DAPR_CONTROLPLANE_NAMESPACE"

	// ControlPlaneAddressEnvVar is the daprd environment variable for
	// configuring the control plane trust domain.
	ControlPlaneTrustDomainEnvVar = "DAPR_CONTROLPLANE_TRUST_DOMAIN"

	// ControlPlaneDefaultTrustAnchorsPath is the default path where the trust anchors are placed for control plane services.
	ControlPlaneDefaultTrustAnchorsPath = "/var/run/secrets/dapr.io/tls/ca.crt"
)
