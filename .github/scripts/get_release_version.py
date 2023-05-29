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
# environment variable, REL_VERSION. If the tag is the final and latest
# version, it sets LATEST_RELEASE to true to add 'latest' tag to docker image.

import os
import sys
import datetime
import glob


def main():
    gitRef = os.getenv("GITHUB_REF")
    tagRefPrefix = "refs/tags/v"

    with open(os.getenv("GITHUB_ENV"), "a") as githubEnv:

        if "schedule" in sys.argv:
            dateTag = datetime.datetime.utcnow().strftime("%Y-%m-%d")
            githubEnv.write("REL_VERSION=nightly-{}\n".format(dateTag))
            print("Nightly release build nightly-{}".format(dateTag))
            sys.exit(0)

        if gitRef is None or not gitRef.startswith(tagRefPrefix):
            githubEnv.write("REL_VERSION=edge\n")
            print("This is daily build from {}...".format(gitRef))
            sys.exit(0)

        releaseVersion = gitRef[len(tagRefPrefix):]
        releaseNoteDir = "docs/release_notes"
        releaseNotePath = "{}/v{}.md".format(
            releaseNoteDir, releaseVersion)

        if gitRef.find("-rc.") > 0:
            print("Release Candidate build from {}...".format(gitRef))
        else:
            print("Checking if {} exists".format(releaseNotePath))
            if os.path.exists(releaseNotePath):
                print("Found {}".format(releaseNotePath))
                # Set LATEST_RELEASE to true
                print("Checking if {} is the latest release".format(
                    releaseVersion))
                if latest_release(releaseNoteDir, releaseVersion):
                    print("Found {} as latest release in {}".format(
                        releaseVersion, releaseNoteDir))
                    githubEnv.write("LATEST_RELEASE=true\n")
                else:
                    print("Release {} is not latest".format(releaseVersion))
            else:
                print("{} is not found".format(releaseNotePath))
                sys.exit(1)
            print("Release build from {}...".format(gitRef))

        githubEnv.write("REL_VERSION={}\n".format(releaseVersion))


def latest_release(releaseNoteDir, releaseVersion):
    semVerPattern = 'v*.md'
    filePaths = glob.glob(releaseNoteDir + "/" + semVerPattern)
    semvers = [path.split('/')[-1].split('.md')[0][1:]
               for path in filePaths if '-rc.' not in path]
    latestRelease = max(semvers, key=extract_version_number)

    print("Found latest release: {}".format(latestRelease))

    if releaseVersion == latestRelease:
        return True
    else:
        return False


def extract_version_number(version):
    return tuple(map(int, version.split('.')))


if __name__ == "__main__":
    main()
