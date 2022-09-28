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

# Allows skipping some E2E test runs on Azure based on OS and arch.
# This is useful when we are still working to stabilize some targets.
# This script allows the stabilization to take place prior to merging into master.
if [ -n "$GITHUB_ENV" ] && [ "$TARGET_OS" = "linux" ] && [ "$TARGET_ARCH" = "arm64" ]; then
  echo "SKIP_E2E=true" >> $GITHUB_ENV
fi