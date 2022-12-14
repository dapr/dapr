#!/usr/bin/env bash

#
# Copyright 2022 The Dapr Authors
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

# Allows custom logic to validate registry readiness.
# This is useful when we are still working to stabilize some targets.
# This script allows the stabilization to take place prior to merging into master.
registry_name=$1
elapsed=0
timeout=300
until [ $elapsed -ge $timeout ] || az acr show --name $registry_name --query "id"
do
  echo "Azure Container Registry not ready yet: sleeping for 20 seconds"
  sleep 20
  elapsed=$(expr $elapsed + 20)
done

if [ $elapsed -ge $timeout ]; then
  echo "Azure Container Registry not ready on time, pushing images might fail in next steps."
fi