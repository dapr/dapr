#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "Usage: $0 <yaml-file> <namespace>" >&2
  exit 2
fi

YAML_FILE="$1"
NAMESPACE="$2"

# Render placeholders and apply.
python3 "$(dirname "$0")/render-yaml.py" "$YAML_FILE" | kubectl apply -f - --namespace "$NAMESPACE"
