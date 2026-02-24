#!/usr/bin/env bash
set -euo pipefail

PARALLELISM="${DAPR_E2E_PARALLELISM:-4}"
NAMESPACE_PREFIX="${DAPR_E2E_NAMESPACE_PREFIX:-dapr-e2e}"
RUN_ID="${DAPR_E2E_RUN_ID:-$(date +%Y%m%d%H%M%S)}"
INFRA_NS="${DAPR_TEST_ENV_NAMESPACE:-${DAPR_TEST_NAMESPACE:-dapr-tests}}"
KEEP_ON_FAIL="${DAPR_E2E_KEEP_NAMESPACES_ON_FAIL:-false}"
GO_TAGS="${DAPR_E2E_GO_TAGS:-e2e}"

# Where to write JSON/JUnit outputs (workflow expects test_report_e2e.*)
OUTPUT_PREFIX="${TEST_OUTPUT_FILE_PREFIX:-./test_report}"

ROOT_CONTAINER_LOG_PATH="${DAPR_CONTAINER_LOG_PATH:-./dist/container_logs}"
ROOT_TEST_LOG_PATH="${DAPR_TEST_LOG_PATH:-./dist/logs}"

# Determine the set of packages to run.
if [[ -n "${DAPR_E2E_TEST:-}" ]]; then
  # DAPR_E2E_TEST can be a single folder name or a space-separated list of folder names.
  PKGS=()
  for t in ${DAPR_E2E_TEST}; do
    # Expand to all packages under that suite
    while IFS= read -r p; do
      [[ -z "$p" ]] && continue
      PKGS+=("$p")
    done < <(
      go list -tags="$GO_TAGS" \
        -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' \
        "./tests/e2e/${t}/..." 2>/dev/null | sed '/^$/d' || true
    )
  done
else
  # Most e2e test packages only contain *_test.go files behind the "e2e" build tag.
  # If we don't include tags here, `go list` will only return helper packages such as tests/e2e/utils
  # and you'll end up running 0 tests.
  mapfile -t PKGS < <(
    go list -tags="$GO_TAGS" \
      -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' \
      ./tests/e2e/... | sed '/^$/d'
  )
fi

if [[ ${#PKGS[@]} -eq 0 ]]; then
  echo "No e2e packages found." >&2
  exit 1
fi

# Ensure infra namespace exists (infra itself is expected to have been installed already by CI,
# but creating the namespace is cheap and idempotent).
kubectl get namespace "$INFRA_NS" >/dev/null 2>&1 || kubectl create namespace "$INFRA_NS" >/dev/null

# Helper to compute namespace for a given package path.
ns_for_pkg() {
  local pkg="$1"
  local h
  h=$(printf '%s' "$pkg" | sha1sum | awk '{print $1}' | cut -c1-8)
  echo "${NAMESPACE_PREFIX}-${h}-${RUN_ID}"
}

run_one() {
  local pkg="$1"
  local ns
  ns=$(ns_for_pkg "$pkg")

  echo "==> [$ns] Running $pkg"

  kubectl create namespace "$ns" >/dev/null 2>&1 || true

  # Apply components/configurations into the package namespace.
  # NOTE: We intentionally disable DAPR_TEST_ID-based suffixing and rely on namespace isolation.
  DAPR_TEST_NAMESPACE="$ns" \
  DAPR_TEST_ENV_NAMESPACE="$INFRA_NS" \
  DAPR_TEST_ID= \
    make -s -f tests/dapr_tests.mk setup-test-components >/dev/null

  local cpath="$ROOT_CONTAINER_LOG_PATH/$ns"
  local tpath="$ROOT_TEST_LOG_PATH/$ns"
  mkdir -p "$cpath" "$tpath"

  # Run the tests.
  # We use gotestsum for consistent output, but isolate logs per namespace.
  set +e
  DAPR_CONTAINER_LOG_PATH="$cpath" \
  DAPR_TEST_LOG_PATH="$tpath" \
  DAPR_TEST_NAMESPACE="$ns" \
  DAPR_TEST_ENV_NAMESPACE="$INFRA_NS" \
  DAPR_TEST_ID= \
    gotestsum \
      --jsonfile "${OUTPUT_PREFIX}_e2e.${ns}.json" \
      --junitfile "${OUTPUT_PREFIX}_e2e.${ns}.xml" \
      --format standard-quiet -- \
        -timeout 20m -count=1 -v -tags="$GO_TAGS" "$pkg" 2>&1 | tee "$tpath/gotestsum.log"
  local rc=${PIPESTATUS[0]}
  set -e

  if [[ $rc -ne 0 ]]; then
    echo "==> [$ns] FAILED $pkg (exit=$rc)"
    if [[ "$KEEP_ON_FAIL" != "true" ]]; then
      kubectl delete namespace "$ns" --wait=false >/dev/null 2>&1 || true
    else
      echo "==> [$ns] Keeping namespace for debugging"
    fi
    return $rc
  fi

  kubectl delete namespace "$ns" --wait=false >/dev/null 2>&1 || true
  echo "==> [$ns] PASSED $pkg"
  return 0
}

export -f ns_for_pkg run_one
export NAMESPACE_PREFIX RUN_ID INFRA_NS KEEP_ON_FAIL ROOT_CONTAINER_LOG_PATH ROOT_TEST_LOG_PATH GO_TAGS OUTPUT_PREFIX

# Run packages in parallel.
printf '%s\n' "${PKGS[@]}" | xargs -P "$PARALLELISM" -I {} bash -lc 'run_one "$@"' _ {}
