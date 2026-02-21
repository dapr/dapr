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

package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// In per-package namespace mode, each test package runs in its own namespace.
// The shared infra (Redis/Kafka/Postgres/Zipkin, etc) is installed in DaprTestNamespaceBase.
// To keep tests working, we replicate the same test components/configurations into each derived namespace,
// patching service addresses so they resolve across namespaces.

var applyStateByNamespace sync.Map // map[string]*applyState

type applyState struct {
	done chan struct{}
	err  error
}

// EnsureTestComponentsApplied installs the standard e2e test components into the given namespace.
// This is only required when running with per-package namespaces.
func EnsureTestComponentsApplied(ctx context.Context, namespace string) error {
	if DaprTestNamespaceMode != DaprTestNamespaceModePerPackage {
		return nil
	}
	if namespace == "" {
		return nil
	}
	if namespace == DaprTestNamespaceBase {
		// The base namespace is assumed to be configured by the Makefile setup steps.
		return nil
	}

	stAny, loaded := applyStateByNamespace.LoadOrStore(namespace, &applyState{done: make(chan struct{})})
	st := stAny.(*applyState)
	if !loaded {
		go func() {
			defer close(st.done)
			st.err = applyTestComponentsIntoNamespace(ctx, namespace)
		}()
	}
	<-st.done
	return st.err
}

func applyTestComponentsIntoNamespace(ctx context.Context, namespace string) error {
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		return fmt.Errorf("kubectl is required to apply test components in per-package namespace mode: %w", err)
	}

	files, err := testComponentFiles()
	if err != nil {
		return err
	}

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("failed to read test component manifest %q: %w", f, err)
		}
		yaml := string(b)
		yaml = patchManifest(yaml, namespace, DaprTestNamespaceBase)
		// Special-case: kubernetes_redis_secret.yaml stores a base64-encoded host string.
		if strings.HasSuffix(f, "kubernetes_redis_secret.yaml") {
			yaml, err = patchRedisHostSecret(yaml, DaprTestNamespaceBase)
			if err != nil {
				return err
			}
		}

		cmd := exec.CommandContext(ctx, kubectl, "apply", "-n", namespace, "-f", "-")
		cmd.Stdin = bytes.NewBufferString(yaml)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to apply %q to namespace %q: %w\n%s", filepath.Base(f), namespace, err, string(out))
		}
	}

	return nil
}

func testComponentFiles() ([]string, error) {
	// Defaults mirror those in tests/dapr_tests.mk.
	stateStore := getenvDefault("DAPR_TEST_STATE_STORE", "postgres")
	queryStateStore := getenvDefault("DAPR_TEST_QUERY_STATE_STORE", "postgres")
	pubsub := getenvDefault("DAPR_TEST_PUBSUB", "redis")
	configStore := getenvDefault("DAPR_TEST_CONFIG_STORE", "redis")
	cryptoProvider := getenvDefault("DAPR_TEST_CRYPTO", "jwks")

	root := testsDir()
	if root == "" {
		return nil, fmt.Errorf("unable to determine repository tests directory")
	}
	configDir := filepath.Join(root, "config")

	paths := []string{
		"dapr_observability_test_config.yaml",
		"kubernetes_secret.yaml",
		"kubernetes_secret_config.yaml",
		"kubernetes_redis_secret.yaml",
		"kubernetes_redis_host_config.yaml",
		fmt.Sprintf("dapr_%s_state.yaml", stateStore),
		fmt.Sprintf("dapr_%s_state_actorstore.yaml", stateStore),
		fmt.Sprintf("dapr_%s_query_state.yaml", queryStateStore),
		"dapr_redis_pluggable_state.yaml",
		"dapr_tests_cluster_role_binding.yaml",
		fmt.Sprintf("dapr_%s_pubsub.yaml", pubsub),
		fmt.Sprintf("dapr_%s_configuration.yaml", configStore),
		"pubsub_resiliency.yaml",
		"kafka_pubsub.yaml",
		fmt.Sprintf("dapr_crypto_%s.yaml", cryptoProvider),
		"dapr_kafka_pluggable_bindings.yaml",
		"dapr_kafka_bindings.yaml",
		"dapr_kafka_bindings_custom_route.yaml",
		"dapr_kafka_bindings_grpc.yaml",
		"app_topic_subscription_pluggable_pubsub.yaml",
		"app_topic_subscription_pubsub.yaml",
		"app_topic_subscription_pubsub_grpc.yaml",
		"kubernetes_allowlists_config.yaml",
		"kubernetes_allowlists_grpc_config.yaml",
		"dapr_redis_state_query.yaml",
		"dapr_redis_state_badhost.yaml",
		"dapr_redis_state_badpass.yaml",
		"dapr_vault_secretstore.yaml",
		"uppercase.yaml",
		"pipeline.yaml",
		"pipeline_app.yaml",
		"preview_configurations.yaml",
		"app_topic_subscription_routing.yaml",
		"app_topic_subscription_routing_grpc.yaml",
		"resiliency.yaml",
		"resiliency_kafka_bindings.yaml",
		"resiliency_kafka_bindings_grpc.yaml",
		fmt.Sprintf("resiliency_%s_pubsub.yaml", pubsub),
		"dapr_in_memory_pubsub.yaml",
		"dapr_in_memory_state.yaml",
		"dapr_tracing_config.yaml",
		"dapr_cron_binding.yaml",
		"external_invocation_http_endpoint.yaml",
		"grpcproxyserverexternal_service.yaml",
		"externalinvocationcrd.yaml",
		"omithealthchecks_config.yaml",
		"external_invocation_http_endpoint_tls.yaml",
	}

	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Join(configDir, p))
	}
	return out, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	// Matches lines like: namespace: "dapr-tests" or namespace: 'dapr-tests'
	reNamespaceDaprTests = regexp.MustCompile(`(?m)^\s*namespace:\s*(["']?)dapr-tests\1\s*$`)
)

func patchManifest(yaml, targetNS, baseNS string) string {
	// 1) Patch well-known cross-namespace endpoints to point to base namespace.
	// Kafka
	yaml = strings.ReplaceAll(yaml, "dapr-kafka:9092", fmt.Sprintf("dapr-kafka.%s.svc.cluster.local:9092", baseNS))
	// Zipkin
	yaml = strings.ReplaceAll(yaml, "http://dapr-zipkin:9411", fmt.Sprintf("http://dapr-zipkin.%s.svc.cluster.local:9411", baseNS))
	yaml = strings.ReplaceAll(yaml, "https://dapr-zipkin:9411", fmt.Sprintf("https://dapr-zipkin.%s.svc.cluster.local:9411", baseNS))
	// Redis (plaintext occurrences)
	yaml = strings.ReplaceAll(yaml, "dapr-redis-master:6379", fmt.Sprintf("dapr-redis-master.%s.svc.cluster.local:6379", baseNS))
	// Replace any hard-coded .dapr-tests.svc.cluster.local occurrences with the base namespace.
	yaml = strings.ReplaceAll(yaml, ".dapr-tests.svc.cluster.local", fmt.Sprintf(".%s.svc.cluster.local", baseNS))

	// 2) Patch allowlists configurations: these require the app namespace, which is the target namespace.
	yaml = reNamespaceDaprTests.ReplaceAllString(yaml, fmt.Sprintf("  namespace: \"%s\"", targetNS))

	return yaml
}

func patchRedisHostSecret(yaml string, baseNS string) (string, error) {
	// The YAML encodes "dapr-redis-master:6379" as base64. We replace it with the fully-qualified address.
	old := "ZGFwci1yZWRpcy1tYXN0ZXI6NjM3OQ==" // dapr-redis-master:6379
	newPlain := fmt.Sprintf("dapr-redis-master.%s.svc.cluster.local:6379", baseNS)
	newEnc := base64.StdEncoding.EncodeToString([]byte(newPlain))
	if !strings.Contains(yaml, old) {
		// If the encoding changes in the future, attempt a best-effort decode+rewrite.
		// This keeps the function resilient without forcing full YAML parsing.
		return yaml, nil
	}
	return strings.ReplaceAll(yaml, old, newEnc), nil
}
