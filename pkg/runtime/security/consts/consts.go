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

/* #nosec. */
const (
	// APITokenEnvVar is the environment variable for the API token.
	APITokenEnvVar = "DAPR_API_TOKEN"
	// AppAPITokenEnvVar is the environment variable for the app API token.
	AppAPITokenEnvVar = "APP_API_TOKEN"
	// SentryLocalIdentityEnvVar is the environment variable for the local identity sent to Sentry.
	SentryLocalIdentityEnvVar = "SENTRY_LOCAL_IDENTITY"
	// SentryTokenEnvVar is the environment variable for the Sentry token file.
	SentryTokenFileEnvVar = "DAPR_SENTRY_TOKEN_FILE"
	// APITokenHeader is header name for HTTP/gRPC calls to hold the token.
	APITokenHeader = "dapr-api-token"
	// Name of the variable injected in the daprd container with the list of injected env vars.
	EnvKeysEnvVar = "DAPR_ENV_KEYS"
)
