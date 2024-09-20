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

# compare_versions returns True if the comparison is successful.
# It returns False if the versions are invalid.
# The result of the comparison is written to the VERSION_UPDATE_REQUIRED GitHub environment variable.
def compare_versions(new_version, existing_version) -> bool:
    try:
        new_ver = Version(new_version)
        existing_ver = Version(existing_version)
    except InvalidVersion:
        print("Invalid version format")
        return False

    update_required = new_ver > existing_ver
    status_message = "New version is greater than existing version." if update_required else "New version is not greater. Skipping update."
    print(status_message)

    # Write the update requirement status to GITHUB_ENV
    github_env_path = os.getenv("GITHUB_ENV")
    if github_env_path:
        with open(github_env_path, "a") as github_env:
            github_env.write(f"VERSION_UPDATE_REQUIRED={str(update_required).lower()}\n")

    return True

if len(sys.argv) != 3:
    print("Usage: compare_versions.py <new_version> <existing_version>")
    sys.exit(1)

new_version = sys.argv[1]
existing_version = sys.argv[2]

if compare_versions(new_version, existing_version):
    sys.exit(0)
else:
    sys.exit(1)
