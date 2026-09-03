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
# Open the post-release-day tasks for a Dapr release.
#
# The tasks live in tasks.json. For each task this script opens one issue in
# the target repository and assigns the Copilot coding agent where the
# repository supports it.
#
# The script is safe to run again. It searches for an open issue with the same
# title before it creates one.
#
# Usage: open-tasks.sh <version>
#   version   the released Dapr version, without a leading v, for example 1.18.4
#
# Environment:
#   GITHUB_TOKEN   a token that can open issues in every target repository
#   DRY_RUN        set to true to print the plan and change nothing

set -euo pipefail

VERSION="${1:?version is required, for example 1.18.4}"
VERSION="${VERSION#v}"
DRY_RUN="${DRY_RUN:-false}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TASKS="${SCRIPT_DIR}/tasks.json"

# The Copilot coding agent is a bot. It can only be assigned through GraphQL,
# and only in repositories where the organisation enabled it.
copilot_bot_id() {
    local owner="$1" name="$2"
    gh api graphql -f query='
        query($owner:String!,$name:String!){
          repository(owner:$owner,name:$name){
            suggestedActors(capabilities:[CAN_BE_ASSIGNED],first:100){
              nodes{ login ... on Bot { id } }
            }
          }
        }' -F owner="$owner" -F name="$name" \
        --jq '.data.repository.suggestedActors.nodes[]
              | select(.login=="copilot-swe-agent") | .id' 2>/dev/null || true
}

assign_copilot() {
    local repo="$1" issue_id="$2" bot_id="$3"
    gh api graphql -f query='
        mutation($assignable:ID!,$actor:ID!){
          replaceActorsForAssignable(input:{assignableId:$assignable,actorIds:[$actor]}){
            assignable { ... on Issue { number } }
          }
        }' -F assignable="$issue_id" -F actor="$bot_id" >/dev/null
}

failed=0
total=$(jq 'length' "$TASKS")

for i in $(seq 0 $((total - 1))); do
    repo=$(jq -r ".[$i].repo" "$TASKS")
    want_copilot=$(jq -r ".[$i].assign_copilot" "$TASKS")
    title=$(jq -r ".[$i].title" "$TASKS" | sed "s/VERSION/${VERSION}/g")
    body=$(jq -r ".[$i].body" "$TASKS" | sed "s/VERSION/${VERSION}/g")
    owner="${repo%%/*}"
    name="${repo##*/}"

    echo "::group::${repo}"

    # Do not open a second issue when one already exists for this version.
    existing=$(gh issue list --repo "$repo" --state open --search "\"$title\" in:title" \
        --json number,title --jq "[.[] | select(.title==\"$title\") | .number] | first // empty" 2>/dev/null || true)
    if [ -n "$existing" ]; then
        echo "issue already open: ${repo}#${existing}"
        echo "::endgroup::"
        continue
    fi

    if [ "$DRY_RUN" = "true" ]; then
        echo "would open: ${title}"
        echo "would assign copilot: ${want_copilot}"
        echo "::endgroup::"
        continue
    fi

    if ! url=$(gh issue create --repo "$repo" --title "$title" --body "$body" 2>&1); then
        echo "::error::could not open an issue in ${repo}: ${url}"
        failed=1
        echo "::endgroup::"
        continue
    fi
    echo "opened ${url}"

    if [ "$want_copilot" != "true" ]; then
        echo "::endgroup::"
        continue
    fi

    bot_id=$(copilot_bot_id "$owner" "$name")
    if [ -z "$bot_id" ]; then
        echo "::warning::the Copilot coding agent is not available in ${repo}, the issue stays unassigned"
        echo "::endgroup::"
        continue
    fi

    number="${url##*/}"
    issue_id=$(gh api "repos/${repo}/issues/${number}" --jq '.node_id')
    if assign_copilot "$repo" "$issue_id" "$bot_id"; then
        echo "assigned the Copilot coding agent"
    else
        echo "::warning::could not assign the Copilot coding agent in ${repo}"
    fi
    echo "::endgroup::"
done

exit "$failed"
