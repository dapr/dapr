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

package kubernetes

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/informer"
	cryptopem "github.com/dapr/kit/crypto/pem"
)

const (
	EnvVarCRDDirectory = "DAPR_INTEGRATION_CRD_DIRECTORY"
)

// Option is a function that configures the mock Kubernetes process.
type Option func(*options)

// Kubernetes is a mock Kubernetes API server process.
type Kubernetes struct {
	http     *prochttp.HTTP
	bundle   bundle.Bundle
	informer *informer.Informer
}

func New(t *testing.T, fopts ...Option) *Kubernetes {
	t.Helper()

	opts := options{
		handlers: make(map[string]http.HandlerFunc),
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	handler := http.NewServeMux()

	handler.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList")
		w.Write([]byte(apiDiscovery))
	})

	handler.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList")
		w.Write([]byte(apisDiscovery))
	})

	handler.HandleFunc("/apis/dapr.io/v1alpha1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(apisDaprV1alpha1))
	})

	handler.HandleFunc("/apis/dapr.io/v2alpha1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(apisDaprV2alpha1))
	})

	handler.HandleFunc("/apis/apps/v1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(apisAppsV1))
	})

	handler.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(apiV1))
	})

	for crdName, crd := range parseCRDs(t) {
		handler.HandleFunc("/apis/apiextensions.k8s.io/v1/customresourcedefinitions/"+crdName, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Content-Type", "application/json")
			w.Write(crd)
		})
	}

	informer := informer.New()

	for path, handle := range opts.handlers {
		handler.HandleFunc(path, informer.Handler(t, handle))
	}

	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	// We need to run the Kubernetes API server with TLS so that HTTP/2.0 is
	// enabled, which is required for informers.
	x509RootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	// Generate a test root key for JWT signing
	jwtRootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      x509RootKey,
		TrustDomain:      "integration.test.dapr.io",
		AllowedClockSkew: time.Second * 5,
		OverrideCATTL:    nil, // Use default CA TTL
	})
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtRootKey,
		TrustDomain: "integration.test.dapr.io",
	})
	require.NoError(t, err)

	leafpk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafCert := &x509.Certificate{
		SerialNumber:       big.NewInt(1),
		NotBefore:          time.Now(),
		NotAfter:           time.Now().Add(time.Minute * 5),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:           []string{"cluster.local"},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	leafCertDER, err := x509.CreateCertificate(rand.Reader, leafCert, x509bundle.IssChain[0], leafpk.Public(), x509bundle.IssKey)
	require.NoError(t, err)
	leafCert, err = x509.ParseCertificate(leafCertDER)
	require.NoError(t, err)

	chainPEM, err := cryptopem.EncodeX509Chain(append([]*x509.Certificate{leafCert}, x509bundle.IssChain...))
	require.NoError(t, err)
	keyPEM, err := cryptopem.EncodePrivateKey(leafpk)
	require.NoError(t, err)

	return &Kubernetes{
		http: prochttp.New(t,
			prochttp.WithHandler(handler),
			prochttp.WithMTLS(t, x509bundle.TrustAnchors, chainPEM, keyPEM),
		),
		bundle: bundle.Bundle{
			X509: x509bundle,
			JWT:  jwtbundle,
		},
		informer: informer,
	}
}

func (k *Kubernetes) Port() int {
	return k.http.Port()
}

func (k *Kubernetes) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	k.http.Run(t, ctx)
}

func (k *Kubernetes) KubeconfigPath(t *testing.T) string {
	t.Helper()

	caPath := filepath.Join(t.TempDir(), "ca.crt")
	certPath := filepath.Join(t.TempDir(), "tls.crt")
	keyPath := filepath.Join(t.TempDir(), "tls.key")
	require.NoError(t, os.WriteFile(caPath, k.bundle.X509.TrustAnchors, 0o600))
	require.NoError(t, os.WriteFile(certPath, k.bundle.X509.IssChainPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, k.bundle.X509.IssKeyPEM, 0o600))

	path := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := fmt.Sprintf(`
apiVersion: v1
kind: Config
clusters:
- name: default
  cluster:
    server: https://localhost:%[1]d
    certificate-authority: %[2]s
    # This is because the sentry CA generative code still marks all issuer
    # certs as 'cluster.local'.
    tls-server-name: cluster.local
contexts:
- name: default
  context:
    cluster: default
    user: default
users:
- name: default
  user:
    client-certificate: %[3]s
    client-key: %[4]s
current-context: default
`, k.Port(), caPath, certPath, keyPath)
	require.NoError(t, os.WriteFile(path, []byte(kubeconfig), 0o600))

	return path
}

func (k *Kubernetes) Informer() *informer.Informer {
	return k.informer
}

func (k *Kubernetes) Cleanup(t *testing.T) {
	t.Helper()
	k.http.Cleanup(t)
}

func parseCRDs(t *testing.T) map[string][]byte {
	t.Helper()

	_, tfile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	defaultPath := filepath.Join(filepath.Dir(tfile), "../../../../../charts/dapr/crds")

	dir, ok := os.LookupEnv(EnvVarCRDDirectory)
	if !ok {
		t.Logf("environment variable %s not set, using default CRD location %s", EnvVarCRDDirectory, defaultPath)
		dir = defaultPath
	}

	crds := make(map[string][]byte)

	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || filepath.Ext(path) != ".yaml" {
			return nil
		}

		f, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var crd apiextensionsv1.CustomResourceDefinition
		if err = yaml.Unmarshal(f, &crd); err != nil {
			return err
		}

		// Set conversion webhook client config to non-nil to allow operator to
		// patch subscriptions.
		crd.Spec.Conversion = new(apiextensionsv1.CustomResourceConversion)
		crd.Spec.Conversion.Webhook = new(apiextensionsv1.WebhookConversion)
		crd.Spec.Conversion.Webhook.ClientConfig = new(apiextensionsv1.WebhookClientConfig)

		fjson, err := json.Marshal(crd)
		if err != nil {
			return err
		}

		crds[crd.Name] = fjson

		return nil
	})

	return crds
}
