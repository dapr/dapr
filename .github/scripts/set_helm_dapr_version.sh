#!/usr/bin/env bash
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

set -e

if [ -z $REL_VERSION ]; then
  echo "REL_VERSION is not set. Exiting ..."
  exit 1
fi

FILES=`grep -Hrl DAPR_VERSION charts`
for file in $FILES; do
  echo "Setting \"version: $REL_VERSION\" in $file ..."
  if [[ "$OSTYPE" == "darwin"* ]]; then
    # Mac OSX
    sed -i '' -e "s/DAPR_VERSION/$REL_VERSION/" "$file"
  else
    # Linux
    sed -e "s/DAPR_VERSION/$REL_VERSION/" -i "$file"
  fi
done
