#
# Copyright 2021 The Dapr Authors
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

# This script automerges PRs in Dapr.

import os

from github import Github


g = Github(os.getenv("GITHUB_TOKEN"))
repo = g.get_repo(os.getenv("GITHUB_REPOSITORY"))
maintainers = [m.strip() for m in os.getenv("MAINTAINERS").split(',')]

def fetch_pulls(mergeable_state, labels = {'automerge'}):
    return [pr for pr in repo.get_pulls(state='open', sort='created') \
        if (not pr.draft) and pr.mergeable_state == mergeable_state and \
            (not labels or len(labels.intersection({l.name for l in pr.labels})) > 0)]

def is_approved(pr):
    approvers = [r.user.login for r in pr.get_reviews() if r.state == 'APPROVED' and r.user.login in maintainers]
    return len([a for a in approvers if repo.get_collaborator_permission(a) in ['admin', 'write']]) > 0

# First, find a PR that can be merged
pulls = fetch_pulls('clean')
print(f"Detected {len(pulls)} open pull requests in {repo.name} to be automerged.")
merged = False
for pr in pulls:
    if is_approved(pr):
        # Merge only one PR per run.
        print(f"Merging PR {pr.html_url}")
        try:
            pr.merge(merge_method='squash')
            merged = True
            break
        except:
            print(f"Failed to merge PR {pr.html_url}")

if len(pulls) > 0 and not merged:
    print("No PR was automerged.")

# Now, update all PRs that are behind, regardless of automerge label.
pulls = fetch_pulls('behind', {'automerge', 'autoupdate'})
print(f"Detected {len(pulls)} open pull requests in {repo.name} to be updated.")
for pr in pulls:
    print(f"Updating PR {pr.html_url}")
    try:
        pr.update_branch()
    except:
        print(f"Failed to update PR {pr.html_url}")

pulls = fetch_pulls('dirty')
print(f"Detected {len(pulls)} open pull requests in {repo.name} to be automerged but are in dirty state.")
for pr in pulls:
    print(f"PR is in dirty state: {pr.html_url}")

print("Done.")
