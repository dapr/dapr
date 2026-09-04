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
# Start the post-release-day tasks for a Dapr release.
#
# For each task in tasks.json this script starts a Copilot cloud agent task in
# the target repository. The agent makes the change and opens the pull request.
#
# The agent tasks API needs a user-to-server token, and the account behind that
# token needs a Copilot seat. When the call fails, the script opens a normal
# issue instead, so the task is never lost.
#
# Usage: open-tasks.sh <version>
#   version   the released Dapr version, without a leading v, for example 1.18.4
#
# Environment:
#   GITHUB_TOKEN   a user-to-server token that can reach every target repository
#   DRY_RUN        set to true to print the plan and change nothing

set -euo pipefail

VERSION="${1:?version is required, for example 1.18.4}"
VERSION="${VERSION#v}"
DRY_RUN="${DRY_RUN:-false}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TASKS="${SCRIPT_DIR}/tasks.json"

API_VERSION_HEADER="X-GitHub-Api-Version: 2022-11-28"

# True when the repository already has an agent task or an open pull request
# for this version. Keeps a re-run from starting the work twice.
already_started() {
    local repo="$1" version="$2"

    if gh api "/agents/repos/${repo}/tasks" -H "$API_VERSION_HEADER" \
        --jq ".tasks[]? | select(.state != \"failed\" and .state != \"cancelled\")
              | select(.name | test(\"${version}\"; \"x\")) | .id" 2>/dev/null | grep -q .; then
        return 0
    fi

    if gh pr list --repo "$repo" --state open --search "$version in:title" \
        --json number --jq 'length' 2>/dev/null | grep -qv '^0$'; then
        return 0
    fi

    return 1
}

start_agent_task() {
    local repo="$1" base="$2" prompt="$3"
    jq -n --arg p "$prompt" --arg b "$base" \
        '{prompt:$p, base_ref:$b, create_pull_request:true}' \
    | gh api -X POST "/agents/repos/${repo}/tasks" -H "$API_VERSION_HEADER" --input - 2>&1
}

failed=0
total=$(jq 'length' "$TASKS")

for i in $(seq 0 $((total - 1))); do
    repo=$(jq -r ".[$i].repo" "$TASKS")
    base=$(jq -r ".[$i].base" "$TASKS")
    title=$(jq -r ".[$i].title" "$TASKS" | sed "s/VERSION/${VERSION}/g")
    body=$(jq -r ".[$i].body" "$TASKS" | sed "s/VERSION/${VERSION}/g")
    prompt="${title}"$'\n\n'"${body}"

    echo "::group::${repo}"

    if already_started "$repo" "$VERSION"; then
        echo "a task or pull request for ${VERSION} already exists, skipping"
        echo "::endgroup::"
        continue
    fi

    if [ "$DRY_RUN" = "true" ]; then
        echo "would start a Copilot agent task on ${repo} (base ${base})"
        echo "prompt starts: ${title}"
        echo "::endgroup::"
        continue
    fi

    if out=$(start_agent_task "$repo" "$base" "$prompt"); then
        url=$(echo "$out" | jq -r '.html_url // empty' 2>/dev/null || true)
        echo "started a Copilot agent task${url:+: $url}"
        echo "::endgroup::"
        continue
    fi

    # The agent could not be started. Common causes are a repository without
    # the Copilot coding agent, or a token that is not user-to-server.
    echo "::warning::could not start a Copilot agent task on ${repo}, opening an issue instead"
    echo "$out" | head -3

    if issue=$(gh issue create --repo "$repo" --title "$title" --body "$body" 2>&1); then
        echo "opened ${issue}"
    else
        echo "::error::could not open an issue in ${repo} either: ${issue}"
        failed=1
    fi
    echo "::endgroup::"
done

exit "$failed"
