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

package sidecar

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/dapr/dapr/pkg/credentials"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
)

// Service represents a Dapr control plane service's information.
type Service struct {
	name string
	port int
}

var (
	// Dapr API service.
	ServiceAPI = Service{"dapr-api", 80}
	// Dapr placement service.
	ServicePlacement = Service{"dapr-placement-server", 50005}
	// Dapr sentry service.
	ServiceSentry = Service{"dapr-sentry", 80}
)

// ServiceAddress returns the address of a Dapr control plane service.
func ServiceAddress(svc Service, namespace string, clusterDomain string) string {
	return fmt.Sprintf("%s.%s.svc.%s:%d", svc.name, namespace, clusterDomain, svc.port)
}

// GetTrustAnchorsAndCertChain returns the trust anchor and certs.
func GetTrustAnchorsAndCertChain(ctx context.Context, kubeClient kubernetes.Interface, namespace string) (string, string, string) {
	secret, err := kubeClient.CoreV1().
		Secrets(namespace).
		Get(ctx, sentryConsts.TrustBundleK8sSecretName, metav1.GetOptions{})
	if err != nil {
		return "", "", ""
	}

	rootCert := secret.Data[credentials.RootCertFilename]
	certChain := secret.Data[credentials.IssuerCertFilename]
	certKey := secret.Data[credentials.IssuerKeyFilename]
	return string(rootCert), string(certChain), string(certKey)
}
