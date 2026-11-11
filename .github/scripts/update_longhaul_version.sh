#!/usr/bin/env bash
#
# Copyright 2026 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Opens or updates the pull request in dapr/test-infra that sets the dapr
# runtime version of the release longhaul cluster to REL_VERSION. Merging the
# pull request deploys the version (dapr-deploy.yml in dapr/test-infra).
#
# The script never lowers the version. A patch release of an older line does
# not replace a later version on master or in the open pull request.
#
# Environment:
#   REL_VERSION   Release version, for example 1.19.0-rc.2.
#   LONGHAUL_DIR  Checkout of dapr/test-infra master with push credentials.
#   GH_TOKEN      Token that can push branches and open pull requests in
#                 dapr/test-infra.

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
LONGHAUL_REPO="${LONGHAUL_REPO:-dapr/test-infra}"
VERSION_FILE="config/dapr_runtime.version"
BRANCH="dapr-bot/longhaul-dapr-runtime-version"

# version_greater NEW OLD succeeds when NEW is a later version than OLD.
version_greater() {
  local env_file
  env_file=$(mktemp)
  if ! GITHUB_ENV="$env_file" python3 "$SCRIPT_DIR/compare_versions.py" "$1" "$2" > /dev/null; then
    echo "::error::Cannot compare the versions '$1' and '$2'"
    exit 1
  fi
  grep -q '^VERSION_UPDATE_REQUIRED=true$' "$env_file"
}

cd "$LONGHAUL_DIR"

master_version=$(tr -d '[:space:]' < "$VERSION_FILE")
echo "Longhaul version on master: $master_version"
if ! version_greater "$REL_VERSION" "$master_version"; then
  echo "$REL_VERSION is not later than $master_version on master. No update."
  exit 0
fi

pr_number=$(gh pr list -R "$LONGHAUL_REPO" --head "$BRANCH" --state open --json number --jq '.[0].number // empty')
if [ -n "$pr_number" ]; then
  git fetch --quiet origin "$BRANCH"
  pr_version=$(git show "FETCH_HEAD:$VERSION_FILE" | tr -d '[:space:]')
  echo "Longhaul version in $LONGHAUL_REPO#$pr_number: $pr_version"
  if ! version_greater "$REL_VERSION" "$pr_version"; then
    echo "$REL_VERSION is not later than $pr_version in $LONGHAUL_REPO#$pr_number. No update."
    exit 0
  fi
fi

git config user.name "dapr-bot"
git config user.email "daprweb@microsoft.com"
git checkout --quiet -B "$BRANCH"
echo "$REL_VERSION" > "$VERSION_FILE"
git commit --quiet --signoff -m "Update the longhaul dapr runtime version to $REL_VERSION" -- "$VERSION_FILE"
git push --quiet --force origin "$BRANCH"

title="Update the longhaul dapr runtime version to $REL_VERSION"
body="The dapr release workflow opened this pull request for the tag v$REL_VERSION: ${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-dapr/dapr}/actions/runs/${GITHUB_RUN_ID:-0}

Merging it deploys dapr $REL_VERSION to the release longhaul cluster (dapr-deploy.yml).

A later release tag updates this pull request. The workflow never sets a version that is lower than the version on master or in this pull request."

if [ -n "$pr_number" ]; then
  gh api -X PATCH "repos/$LONGHAUL_REPO/pulls/$pr_number" -f title="$title" -f body="$body" > /dev/null
  echo "Updated $LONGHAUL_REPO#$pr_number to $REL_VERSION"
else
  gh pr create -R "$LONGHAUL_REPO" --base master --head "$BRANCH" --title "$title" --body "$body"
fi
