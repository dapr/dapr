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
import datetime

gitRef = os.getenv("GITHUB_REF")
tagRefPrefix = "refs/tags/v"

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
