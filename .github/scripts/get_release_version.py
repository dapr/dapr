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
import sys

gitRef = os.getenv("GITHUB_REF")
tagRefPrefix = "refs/tags/v"
nightlyTagPrefix = "refs/tags/nightly"

with open(os.getenv("GITHUB_ENV"), "a") as githubEnv:

    if gitRef is None or not gitRef.startswith((tagRefPrefix, nightlyTagPrefix)):
        githubEnv.write("REL_VERSION=edge\n")
        githubEnv.write("RELEASE_TO_GH=False\n")
        print ("This is daily build from {}...".format(gitRef))
        sys.exit(0)

    githubEnv.write("RELEASE_TO_GH=True\n")

    if gitRef.find("nightly") > 0:
        print ("Nightly build for {}...".format(gitRef))
        releaseVersion = gitRef[len("refs/tags/"):]
        githubEnv.write("REL_VERSION={}\n".format(releaseVersion))
        githubEnv.write("REL_TAG={}\n".format(releaseVersion))
        sys.exit(0)

    releaseVersion = gitRef[len(tagRefPrefix):]
    releaseNotePath="docs/release_notes/v{}.md".format(releaseVersion)

    if gitRef.find("-rc.") > 0:
        print ("Release Candidate build from {}...".format(gitRef))
    else:
        print ("Checking if {} exists".format(releaseNotePath))
        if os.path.exists(releaseNotePath):
            print ("Found {}".format(releaseNotePath))
            # Set LATEST_RELEASE to true
            githubEnv.write("LATEST_RELEASE=true\n")
        else:
            print ("{} is not found".format(releaseNotePath))
            sys.exit(1)
        print ("Release build from {}...".format(gitRef))

    githubEnv.write("REL_VERSION={}\n".format(releaseVersion))
    githubEnv.write("REL_TAG=v{}\n".format(releaseVersion))
