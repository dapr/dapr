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

# This script parses release version from Git tag and set the parsed version to
# environment variable, REL_VERSION. If the tag is the final version, it sets
# LATEST_RELEASE to true to add 'latest' tag to docker image.

import os
import re
import sys
import glob
import datetime

gitRef = os.getenv("GITHUB_REF")
tagRefPrefix = "refs/tags/v"


# parseVersion returns a (major, minor, patch) tuple of ints for a final
# release version such as "1.9.5". It returns None for pre-release versions
# (e.g. "1.0.0-rc.1") or anything it cannot parse, so that only final releases
# are considered when determining the 'latest' tag.
def parseVersion(version):
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", version)
    if match is None:
        return None
    return tuple(int(part) for part in match.groups())


# isLatestRelease returns True only if the given final release version is the
# highest one across all release notes in releaseNotesDir. This prevents a
# hotfix to an older release branch (e.g. 1.9.6 published after 1.10.0) from
# being tagged as 'latest' just because it is the most recent chronologically.
def isLatestRelease(version, releaseNotesDir):
    current = parseVersion(version)
    if current is None:
        return False
    for notePath in glob.glob(os.path.join(releaseNotesDir, "v*.md")):
        # Strip the leading 'v' and trailing '.md' from the file name.
        other = parseVersion(os.path.basename(notePath)[1:-3])
        if other is not None and other > current:
            return False
    return True

with open(os.getenv("GITHUB_ENV"), "a") as githubEnv:

    if "schedule" in sys.argv:
        dateTag = datetime.datetime.utcnow().strftime("%Y-%m-%d")
        githubEnv.write("REL_VERSION=nightly-{}\n".format(dateTag))
        print ("Nightly release build nightly-{}".format(dateTag))
        sys.exit(0)

    if gitRef is None or not gitRef.startswith(tagRefPrefix):
        githubEnv.write("REL_VERSION=edge\n")
        print ("This is daily build from {}...".format(gitRef))
        sys.exit(0)

    releaseVersion = gitRef[len(tagRefPrefix):]
    releaseNotePath="docs/release_notes/v{}.md".format(releaseVersion)

    if gitRef.find("-rc.") > 0:
        print ("Release Candidate build from {}...".format(gitRef))
    elif gitRef.find("-alpha.") > 0:
        print ("Release Alpha build from {}...".format(gitRef))
        githubEnv.write("ALPHA_RELEASE=true\n")
    else:
        print ("Checking if {} exists".format(releaseNotePath))
        if os.path.exists(releaseNotePath):
            print ("Found {}".format(releaseNotePath))
            # Only tag as 'latest' if this is the highest release version, so
            # that hotfixes to older branches do not overwrite the 'latest' tag.
            if isLatestRelease(releaseVersion, os.path.dirname(releaseNotePath)):
                print ("{} is the latest release; setting LATEST_RELEASE=true".format(releaseVersion))
                githubEnv.write("LATEST_RELEASE=true\n")
            else:
                print ("{} is not the latest release; skipping 'latest' tag".format(releaseVersion))
        else:
            print ("{} is not found".format(releaseNotePath))
            sys.exit(1)
        print ("Release build from {}...".format(gitRef))

    githubEnv.write("REL_VERSION={}\n".format(releaseVersion))
