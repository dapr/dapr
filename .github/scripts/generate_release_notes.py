# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script finds all release notes from issues in the milestone project.

import os
import re
import sys

from datetime import date
from string import Template

from github import Github

milestoneProjectRegex = "^(.*) Milestone( [0-9])?$"
releaseNoteRegex = "^RELEASE NOTE:(.*)$"
dashboardReleaseVersionRegex = "v([0-9\.]+)-?.*"
majorReleaseRegex = "^([0-9]+\.[0-9]+)\.[0-9]+.*$"

githubToken = os.getenv("GITHUB_TOKEN")

top_repos=[
    'dapr',
    'cli',
    'components-contrib',
    'dashboard',
    'dotnet-sdk',
    'go-sdk',
    'java-sdk',
    'python-sdk',
    'rust-sdk',
    'cpp-sdk',
    'js-sdk',
    'workflows',
    'docs',
    'quickstarts',
    'samples']

subtitles={
    "dapr": "Dapr Runtime",
    "cli": "Dapr CLI",
    "dashboard": "Dashboard",
    "components-contrib": "Components",
    "java-sdk": "Java SDK",
    "dotnet-sdk": ".NET SDK",
    "go-sdk": "Go SDK",
    "python-sdk": "Python SDK",
    "js-sdk": "JavaScript SDK",
    "rust-sdk": "Rust SDK",
    "cpp-sdk": "C++ SDK",
    "docs": "Documentation",
}

changes=[]

def get_repo_subtitle(name):
    if name in subtitles:
        return subtitles[name]
    return name.capitalize()

def get_repo_priority(name):
    if name in top_repos:
        return top_repos.index(name)
    return len(top_repos)

# using an access token
g = Github(githubToken)

org = g.get_organization("dapr")

# discover milestone project
projects = [p for p in org.get_projects(state='open') if re.search(milestoneProjectRegex, p.name)]
projects = sorted(projects, key=lambda p: p.id)
if len(projects) == 0:
    print("FATAL: could not find project for milestone to be released.")
    sys.exit(0)

if len(projects) > 1:
    print("WARNING: found more than one project for release, so first project created will be picked: {}".format(
        [p.name for p in projects]))

project = projects[0]
print("Found project: {}".format(project.name))

# get release version from project name
releaseVersion = re.search(milestoneProjectRegex, project.name).group(1)
print("Generating release notes for Dapr {}...".format(releaseVersion))
# Set REL_VERSION.
if os.getenv("GITHUB_ENV"):
    with open(os.getenv("GITHUB_ENV"), "a") as githubEnv:
        githubEnv.write("REL_VERSION={}\n".format(releaseVersion))
        githubEnv.write("REL_BRANCH=release-{}\n".format(
            re.search(majorReleaseRegex, releaseVersion).group(1)))

# get dashboard release version
releases = sorted([r for r in g.get_repo("dapr/dashboard").get_releases()], key=lambda r: r.created_at, reverse=True)
dashboardReleaseVersion = re.search(dashboardReleaseVersionRegex, releases[0].tag_name).group(1)
print("Detected Dapr Dashboard version {}".format(dashboardReleaseVersion))

# get all cards in all columns
columns = project.get_columns()
cards = []
for column in columns:
    cards = cards + [c for c in column.get_cards()]

# generate changes
for c in cards:
    issueOrPR = c.get_content()
    contributors = []
    url = issueOrPR.html_url
    try:
        # only a PR can be converted to a PR object, otherwise will throw error.
        pr = issueOrPR.as_pull_request()
        contributors.append(pr.user.login)
    except:
        a = [l.login for l in issueOrPR.assignees]
        if len(a) == 0:
            print("Issue is unassigned: {}".format(url))
        contributors.extend(a)
    match = re.search(releaseNoteRegex, issueOrPR.body, re.M)
    hasNote = False
    if match:
        repo = issueOrPR.repository
        note = match.group(1).strip()
        if note:
            if note.upper() not in ["NOT APPLICABLE", "N/A"]:
                changes.append((repo, issueOrPR, note, contributors, url))
            hasNote = True
    if not hasNote:
        assignee = 'nobody'
        if issueOrPR.assignee:
            assignee = issueOrPR.assignee.login
        print("Issue or PR assigned to {} has no release note: {}".format(assignee, url))

warnings=[]
contributors = set()
changeLines=[]
lastSubtitle=""

breakingChangeLines=[]
lastBreakingChangeSubtitle=""

# generate changes for relase notes
for change in sorted(changes, key=lambda c: (get_repo_priority(c[0].name), c[0].stargazers_count * -1, c[0].id, c[1].id)):
    breakingChange='breaking-change' in [l.name for l in change[1].labels]
    subtitle=get_repo_subtitle(change[0].name)
    if lastSubtitle != subtitle:
        lastSubtitle = subtitle
        changeLines.append("### " + subtitle)
    # set issue url
    changeUrl = " [" + str(change[1].number) + "](" + change[4] + ")"
    changeLines.append("- " + change[2] + changeUrl)
    
    # Add all contributors to a set
    for c in change[3]:
        contributors.add("@" + str(c))

    if breakingChange:
        if lastBreakingChangeSubtitle != subtitle:
            lastBreakingChangeSubtitle = subtitle
            breakingChangeLines.append("### " + subtitle)
        breakingChangeLines.append("- " + change[2] + changeUrl)

if len(breakingChangeLines) > 0:
    warnings.append("> **Note: This release contains a few [breaking changes](#breaking-changes).**")

# generate release notes from template
template=''
releaseNoteTemplatePath="docs/release_notes/template.md"
with open(releaseNoteTemplatePath, 'r') as file:
    template = file.read()

changesText='\n\n'.join(changeLines)
breakingChangesText='None.'
if len(breakingChangeLines) > 0:
    breakingChangesText='\n\n'.join(breakingChangeLines)
warningsText=''
if len(warnings) > 0:
    warningsText='\n\n'.join(warnings)

releaseNotePath="docs/release_notes/v{}.md".format(releaseVersion)
with open(releaseNotePath, 'w') as file:
    file.write(Template(template).safe_substitute(
        dapr_version=releaseVersion,
        dapr_dashboard_version=dashboardReleaseVersion,
        dapr_changes=changesText,
        dapr_breaking_changes=breakingChangesText,
        warnings=warningsText,
        dapr_contributors=", ".join(sorted(list(contributors))),
        today=date.today().strftime("%Y-%m-%d")))
print("Done.")
