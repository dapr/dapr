#!/usr/bin/env bash
#
# Copyright 2023 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -ue

# Thanks to https://ihateregex.io/expr/semver/
SEMVER_REGEX='^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$'

REL_VERSION=`echo $1 | sed -r 's/^[vV]?([0-9].+)$/\1/'`

if [ `echo $REL_VERSION | pcre2grep "$SEMVER_REGEX"` ]; then
  echo "$REL_VERSION is a valid semantic version."
else
  echo "$REL_VERSION is not a valid semantic version."
  exit 1
fi

MAJOR_MINOR_VERSION=`echo $REL_VERSION | cut -d. -f1,2`
RELEASE_BRANCH="release-$MAJOR_MINOR_VERSION"
RELEASE_TAG="v$REL_VERSION"

if [ `git rev-parse --verify origin/$RELEASE_BRANCH 2>/dev/null` ]; then
  echo "$RELEASE_BRANCH branch already exists, checking it out ..."
  git checkout $RELEASE_BRANCH
else
  echo "$RELEASE_BRANCH does not exist, creating ..."
  git checkout -b $RELEASE_BRANCH
  git push origin $RELEASE_BRANCH
fi
echo "$RELEASE_BRANCH branch is ready."

if [ `git rev-parse --verify $RELEASE_TAG 2>/dev/null` ]; then
  echo "$RELEASE_TAG tag already exists, aborting ..."
  exit 2
fi

echo "Tagging $RELEASE_TAG ..."
git tag $RELEASE_TAG
echo "$RELEASE_TAG is tagged."

echo "Pushing $RELEASE_TAG tag ..."
git push origin $RELEASE_TAG
echo "$RELEASE_TAG tag is pushed."
