#
# Copyright 2024 The Dapr Authors
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
from packaging.version import Version, InvalidVersion

def compare_versions(new_version, existing_version):
    try:
        new_ver = Version(new_version)
        existing_ver = Version(existing_version)
    except InvalidVersion:
        print("Invalid version format")
        sys.exit(1)

    if new_ver > existing_ver:
        print("New version is greater than existing version.")
        with open(os.getenv("GITHUB_ENV"), "a") as github_env:
            github_env.write("UPDATE_REQUIRED=true\n")

    else:
        print("New version is not greater. Skipping update.")
        with open(os.getenv("GITHUB_ENV"), "a") as github_env:
            github_env.write("UPDATE_REQUIRED=false\n")
    sys.exit(0)

if len(sys.argv) != 3:
    print("Usage: compare_versions.py <new_version> <existing_version>")
    sys.exit(1)

new_version = sys.argv[1]
existing_version = sys.argv[2]

compare_versions(new_version, existing_version)
