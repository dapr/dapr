/*
Copyright 2023 The Dapr Authors
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

package token

import (
	"errors"
	"fmt"
	"os"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

const (
	kubeTknPath       = "/var/run/secrets/dapr.io/sentrytoken/token"
	legacyKubeTknPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

var log = logger.NewLogger("dapr.security.token")

// GetSentryToken returns the token for authenticating with Sentry.
func GetSentryToken(allowKubernetes bool) (token string, validator sentryv1pb.SignCertificateRequest_TokenValidator, err error) {
	var b []byte

	// Check if we have a token file in the DAPR_SENTRY_TOKEN_FILE env var (for the JWKS validator)
	if path, ok := os.LookupEnv(securityConsts.SentryTokenFileEnvVar); ok {
		if path == "" {
			return "", sentryv1pb.SignCertificateRequest_UNKNOWN, errors.New("environmental variable DAPR_SENTRY_TOKEN_FILE is set with an empty value")
		}
		b, err = os.ReadFile(path)
		if err != nil {
			log.Warnf("Failed to read token at path '%s': %v", path, err)
			return "", sentryv1pb.SignCertificateRequest_UNKNOWN, fmt.Errorf("failed to read token at path '%s': %w", path, err)
		}
		if len(b) == 0 {
			log.Warnf("Token at path '%s' is empty", path)
			return "", sentryv1pb.SignCertificateRequest_UNKNOWN, fmt.Errorf("token at path '%s' is empty", path)
		}

		log.Debugf("Loaded token from path '%s' specified in the DAPR_SENTRY_TOKEN_FILE environmental variable", path)
		return string(b), sentryv1pb.SignCertificateRequest_JWKS, nil
	}

	if allowKubernetes {
		// Try to read a token from Kubernetes (for the Kubernetes validator)
		b, err = os.ReadFile(kubeTknPath)
		if err != nil && os.IsNotExist(err) {
			// Attempt to use the legacy token if that exists
			b, _ = os.ReadFile(legacyKubeTknPath)
			if len(b) > 0 {
				log.Warn("⚠️ daprd is initializing using the legacy service account token with access to Kubernetes APIs, which is discouraged. This usually happens when daprd is running against an older version of the Dapr control plane.")
			}
		}
		if len(b) > 0 {
			return string(b), sentryv1pb.SignCertificateRequest_KUBERNETES, nil
		}
	}

	return "", sentryv1pb.SignCertificateRequest_UNKNOWN, nil
}

// HasKubernetesToken returns true if a Kubernetes token exists.
func HasKubernetesToken() bool {
	_, err := os.Stat(kubeTknPath)
	if err != nil {
		_, err = os.Stat(legacyKubeTknPath)
		if err != nil {
			return false
		}
	}
	return true
}
