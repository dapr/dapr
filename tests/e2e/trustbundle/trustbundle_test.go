//go:build e2e

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

package trustbundle_e2e

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	numHealthChecks = 60

	callerApp = "trustbundle-caller"
	calleeApp = "trustbundle-callee"

	// trustAnchorsConfigMapName is the per-namespace ConfigMap written by the
	// operator holding the CA trust anchors.
	trustAnchorsConfigMapName = "dapr-root-ca.crt"
	// trustBundleName is the control plane Secret and ConfigMap holding the
	// CA credentials and public trust anchors.
	trustBundleName = "dapr-trust-bundle"
)

var (
	tr *runner.TestRunner

	// A namespace which only exists for this test, proving the operator
	// distributes the trust anchors ConfigMap into new namespaces.
	secondaryNamespace = "dapr-tests-trustbundle"
)

type testCommandRequest struct {
	RemoteApp        string `json:"remoteApp,omitempty"`
	Method           string `json:"method,omitempty"`
	RemoteAppTracing string `json:"remoteAppTracing"`
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

func TestMain(m *testing.M) {
	utils.SetupLogs("trustbundle")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:        callerApp,
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        calleeApp,
			DaprEnabled:    true,
			ImageName:      "e2e-service_invocation",
			Replicas:       1,
			MetricsEnabled: true,
			Namespace:      &secondaryNamespace,
		},
	}

	tr = runner.NewTestRunner("trustbundle", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func controlPlaneNamespace() string {
	if ns := os.Getenv("DAPR_NAMESPACE"); ns != "" {
		return ns
	}
	return "dapr-system"
}

// countPEMCertificates counts the CERTIFICATE blocks in a PEM bundle.
func countPEMCertificates(data []byte) int {
	var count int
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return count
		}
		if block.Type == "CERTIFICATE" {
			count++
		}
	}
}

// invokeCallee invokes the callee app in the secondary namespace through the
// caller's daprd, exercising mTLS between the two sidecars.
func invokeCallee(externalURL string) error {
	body, err := json.Marshal(testCommandRequest{
		RemoteApp: fmt.Sprintf("%s.%s", calleeApp, secondaryNamespace),
		Method:    "singlehop",
	})
	if err != nil {
		return err
	}

	resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf("http://%s/tests/invoke_test", externalURL), body)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("expected status 200, got %d: %s", status, string(resp))
	}

	var appResp appResponse
	if err := json.Unmarshal(resp, &appResp); err != nil {
		return err
	}
	if appResp.Message != "singlehop is called" {
		return fmt.Errorf("unexpected response message %q", appResp.Message)
	}

	return nil
}

// TestTrustDistribution verifies the operator distributes the trust anchors
// ConfigMap into every namespace, the injected daprd consumes it from the
// mounted file, and mTLS service invocation works across namespaces on the
// distributed anchors.
func TestTrustDistribution(t *testing.T) {
	platform, ok := tr.Platform.(*runner.KubeTestPlatform)
	if !ok {
		t.Skip("skipping test; only supported on kubernetes")
	}
	ctx := t.Context()

	externalURL := platform.AcquireAppExternalURL(callerApp)
	require.NotEmpty(t, externalURL, "external URL must not be empty")
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	kc := platform.KubeClient

	sourceCM, err := kc.ClientSet.CoreV1().ConfigMaps(controlPlaneNamespace()).Get(ctx, trustBundleName, metav1.GetOptions{})
	require.NoError(t, err)
	sourceAnchors := sourceCM.Data["ca.crt"]
	require.NotEmpty(t, sourceAnchors)

	t.Run("trust anchors ConfigMap is distributed to every namespace", func(t *testing.T) {
		for _, ns := range []string{kube.DaprTestNamespace, secondaryNamespace} {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				cm, cerr := kc.ClientSet.CoreV1().ConfigMaps(ns).Get(ctx, trustAnchorsConfigMapName, metav1.GetOptions{})
				if !assert.NoError(c, cerr) {
					return
				}
				assert.Equal(c, sourceAnchors, cm.Data["ca.crt"], "namespace %q", ns)
			}, time.Minute, time.Second)
		}
	})

	t.Run("daprd consumes the trust anchors from the mounted file", func(t *testing.T) {
		pods, perr := kc.Pods(kube.DaprTestNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", kube.TestAppLabelKey, callerApp),
		})
		require.NoError(t, perr)
		require.NotEmpty(t, pods.Items)
		pod := pods.Items[0]

		var daprd *apiv1.Container
		containers := make([]apiv1.Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
		containers = append(containers, pod.Spec.Containers...)
		containers = append(containers, pod.Spec.InitContainers...)
		for i, container := range containers {
			if container.Name == "daprd" {
				daprd = &containers[i]
				break
			}
		}
		require.NotNil(t, daprd, "daprd container not found in pod %q", pod.Name)

		var foundEnv, foundMount, foundVolume bool
		for _, env := range daprd.Env {
			if env.Name == "DAPR_TRUST_ANCHORS_FILE" {
				assert.Equal(t, "/var/run/secrets/dapr.io/tls/ca.crt", env.Value)
				foundEnv = true
			}
		}
		for _, mount := range daprd.VolumeMounts {
			if mount.Name == "dapr-trust-anchors" {
				assert.Equal(t, "/var/run/secrets/dapr.io/tls", mount.MountPath)
				foundMount = true
			}
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.Name == "dapr-trust-anchors" {
				if assert.NotNil(t, volume.ConfigMap) {
					assert.Equal(t, trustAnchorsConfigMapName, volume.ConfigMap.Name)
				}
				foundVolume = true
			}
		}
		assert.True(t, foundEnv, "daprd should have the trust anchors file env var")
		assert.True(t, foundMount, "daprd should mount the trust anchors volume")
		assert.True(t, foundVolume, "pod should have the trust anchors ConfigMap volume")
	})

	t.Run("cross namespace service invocation works on the distributed anchors", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NoError(c, invokeCallee(externalURL))
		}, time.Minute, time.Second*2)
	})
}

// TestCARenewal patches sentry to renew immediately and verifies the
// append-and-propagate half of the rollover on a real cluster: a new trust
// anchor is appended to the trust bundle (the old anchor is retained), the
// pending issuer pair is stored, the appended anchors are re-distributed to
// the per-namespace ConfigMaps, and service invocation keeps working, all
// without restarting any workload.
//
// The propagation grace is deliberately far longer than the test so the
// renewal stays PENDING throughout: the signing key never changes, which
// keeps this test safe for the shared e2e control plane (test packages run
// concurrently with -p 2). Renewals never recur while one is pending, so
// exactly one anchor is appended. The switchover itself is exercised by the
// sentry/ca/renewal integration suite, where the whole environment is
// private; switching signing keys here with the e2e kubelet ConfigMap
// propagation delay would break sidecars deployed concurrently by other
// test packages.
//
// The original sentry configuration is restored on completion. Restoring
// while pending is safe: the restored sentry resumes the pending state with
// the default 24h grace and keeps signing with the old, fully distributed
// issuer.
func TestCARenewal(t *testing.T) {
	platform, ok := tr.Platform.(*runner.KubeTestPlatform)
	if !ok {
		t.Skip("skipping test; only supported on kubernetes")
	}
	ctx := t.Context()
	namespace := controlPlaneNamespace()
	kc := platform.KubeClient

	externalURL := platform.AcquireAppExternalURL(callerApp)
	require.NotEmpty(t, externalURL, "external URL must not be empty")
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	secretBefore, err := kc.ClientSet.CoreV1().Secrets(namespace).Get(ctx, trustBundleName, metav1.GetOptions{})
	require.NoError(t, err)
	issuerBefore := string(secretBefore.Data["issuer.crt"])
	anchorsBefore := secretBefore.Data["ca.crt"]
	countBefore := countPEMCertificates(anchorsBefore)
	require.NotZero(t, countBefore)

	deployments := kc.ClientSet.AppsV1().Deployments(namespace)

	waitForRollout := func(t *testing.T) {
		t.Helper()
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			deploy, derr := deployments.Get(ctx, "dapr-sentry", metav1.GetOptions{})
			if !assert.NoError(c, derr) {
				return
			}
			replicas := int32(1)
			if deploy.Spec.Replicas != nil {
				replicas = *deploy.Spec.Replicas
			}
			assert.GreaterOrEqual(c, deploy.Status.ObservedGeneration, deploy.Generation)
			assert.Equal(c, replicas, deploy.Status.UpdatedReplicas)
			assert.Equal(c, replicas, deploy.Status.ReadyReplicas)
		}, time.Minute*3, time.Second)
	}

	// Patch sentry to renew immediately with a short propagation grace.
	// Sentry uses pflag, where the last occurrence of a flag wins, so
	// appending overrides any values from the helm chart.
	var originalArgs []string
	require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, derr := deployments.Get(ctx, "dapr-sentry", metav1.GetOptions{})
		if derr != nil {
			return derr
		}
		originalArgs = deploy.Spec.Template.Spec.Containers[0].Args
		patched := make([]string, 0, len(originalArgs)+3)
		patched = append(patched, originalArgs...)
		patched = append(patched,
			"--ca-renewal-enabled=true",
			"--ca-renewal-threshold=0.000001",
			// Far longer than the test: the renewal must stay pending so the
			// signing key never changes on the shared control plane.
			"--trust-anchor-propagation-grace=1h",
		)
		deploy.Spec.Template.Spec.Containers[0].Args = patched
		_, derr = deployments.Update(ctx, deploy, metav1.UpdateOptions{})
		return derr
	}))
	t.Cleanup(func() {
		// Restore the original sentry configuration. The appended anchor and
		// pending issuer pair are left in place: they are harmless by design
		// (the restored sentry resumes the pending state with the default 24h
		// grace and keeps signing with the old issuer), and removing them
		// would invalidate the renewed state.
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute*4)
		defer cancel()
		require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
			deploy, derr := deployments.Get(cctx, "dapr-sentry", metav1.GetOptions{})
			if derr != nil {
				return derr
			}
			deploy.Spec.Template.Spec.Containers[0].Args = originalArgs
			_, derr = deployments.Update(cctx, deploy, metav1.UpdateOptions{})
			return derr
		}))

		// Wait for the restored sentry to be ready and self-consistent so a
		// torn Secret/ConfigMap write during the rollout cannot leak into
		// test packages running after this one: the restored sentry heals the
		// ConfigMap from the Secret on startup.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			deploy, derr := deployments.Get(cctx, "dapr-sentry", metav1.GetOptions{})
			if !assert.NoError(c, derr) {
				return
			}
			replicas := int32(1)
			if deploy.Spec.Replicas != nil {
				replicas = *deploy.Spec.Replicas
			}
			assert.GreaterOrEqual(c, deploy.Status.ObservedGeneration, deploy.Generation)
			assert.Equal(c, replicas, deploy.Status.UpdatedReplicas)
			assert.Equal(c, replicas, deploy.Status.ReadyReplicas)

			secret, serr := kc.ClientSet.CoreV1().Secrets(namespace).Get(cctx, trustBundleName, metav1.GetOptions{})
			if !assert.NoError(c, serr) {
				return
			}
			cm, cerr := kc.ClientSet.CoreV1().ConfigMaps(namespace).Get(cctx, trustBundleName, metav1.GetOptions{})
			if !assert.NoError(c, cerr) {
				return
			}
			assert.Equal(c, string(secret.Data["ca.crt"]), cm.Data["ca.crt"], "trust bundle ConfigMap must be consistent with the Secret")
		}, time.Minute*3, time.Second)
	})
	waitForRollout(t)

	t.Run("a renewed trust anchor is appended, the old anchor is retained", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			secret, serr := kc.ClientSet.CoreV1().Secrets(namespace).Get(ctx, trustBundleName, metav1.GetOptions{})
			if !assert.NoError(c, serr) {
				return
			}
			anchors := secret.Data["ca.crt"]
			if !assert.Greater(c, countPEMCertificates(anchors), countBefore, "expected an appended trust anchor") {
				return
			}
			assert.Equal(c, string(anchorsBefore), string(anchors[:len(anchorsBefore)]), "old anchors must be retained byte for byte")
		}, time.Minute*2, time.Second)
	})

	t.Run("appended anchors are re-distributed to the per-namespace ConfigMaps", func(t *testing.T) {
		for _, ns := range []string{kube.DaprTestNamespace, secondaryNamespace} {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				cm, cerr := kc.ClientSet.CoreV1().ConfigMaps(ns).Get(ctx, trustAnchorsConfigMapName, metav1.GetOptions{})
				if !assert.NoError(c, cerr) {
					return
				}
				assert.Greater(c, countPEMCertificates([]byte(cm.Data["ca.crt"])), countBefore, "namespace %q", ns)
			}, time.Minute*2, time.Second)
		}
	})

	t.Run("pending issuer pair is stored and the signing issuer is unchanged", func(t *testing.T) {
		secret, serr := kc.ClientSet.CoreV1().Secrets(namespace).Get(ctx, trustBundleName, metav1.GetOptions{})
		require.NoError(t, serr)
		assert.Contains(t, secret.Data, "issuer.next.crt")
		assert.Contains(t, secret.Data, "issuer.next.key")
		assert.Equal(t, issuerBefore, string(secret.Data["issuer.crt"]), "sentry must keep signing with the old issuer during the grace")
	})

	t.Run("service invocation works while the renewal is pending", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NoError(c, invokeCallee(externalURL))
		}, time.Minute, time.Second*2)
	})
}
