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

replace_all() {
  SUBSTRING=$1
  CONTENT=$2
  FILES=$(grep -Hrl $SUBSTRING charts)
  for file in $FILES; do
    echo "Replacing \"$SUBSTRING\" with \"$CONTENT\" in $file ..."
    if [[ "$OSTYPE" == "darwin"* ]]; then
      # Mac OSX
      sed -i '' -e "s/$SUBSTRING/$CONTENT/" "$file"
    else
      # Linux
      sed -e "s/$SUBSTRING/$CONTENT/" -i "$file"
    fi
  done
}

revert_replace_all_chart_yaml() {
  SUBSTRING=$1
  CONTENT=$2
  FILES=$(grep -Hrl $SUBSTRING charts)
  for file in $FILES; do
    if [[ "$file" =~ "Chart.yaml" ]]; then
      echo "Replacing \"$SUBSTRING\" with \"$CONTENT\" in $file ..."
      if [[ "$OSTYPE" == "darwin"* ]]; then
        # Mac OSX
        sed -i '' -e "s/$SUBSTRING/$CONTENT/" "$file"
      else
        # Linux
        sed -e "s/$SUBSTRING/$CONTENT/" -i "$file"
      fi
    fi
  done
}

revert_replace_all_values_yaml() {
  SUBSTRING=$1
  CONTENT=$2
  FILES=$(grep -Hrl $SUBSTRING charts)
  for file in $FILES; do
    if [[ "$file" =~ "values.yaml" ]]; then
      echo "Replacing \"$SUBSTRING\" with \"$CONTENT\" in $file ..."
      if [[ "$OSTYPE" == "darwin"* ]]; then
        # Mac OSX
        sed -i '' -e "s/$SUBSTRING/$CONTENT/" "$file"
      else
        # Linux
        sed -e "s/$SUBSTRING/$CONTENT/" -i "$file"
      fi
    fi
  done
}

if [ -z $REL_VERSION ]; then
  echo "REL_VERSION is not set. Exiting ..."
  exit 1
fi

DAPR_VERSION_HELM="${REL_VERSION}"
DAPR_VERSION_TAG="${REL_VERSION}"
if [[ "$REL_VERSION" == "edge" || "$REL_VERSION" == "nightly"* ]]; then
  DAPR_VERSION_HELM="0.0.0"
fi

OPERATING=$1

if [[ "$OPERATING" == "set" ]]; then
  replace_all "'edge'" "'$DAPR_VERSION_TAG'"
  replace_all "'0.0.0'" "'$DAPR_VERSION_HELM'"
elif [[ "$OPERATING" == "revert" ]]; then
  revert_replace_all_values_yaml "$DAPR_VERSION_TAG" "edge"
  revert_replace_all_chart_yaml "$DAPR_VERSION_HELM" "0.0.0"
fi
