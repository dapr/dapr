#!/usr/bin/env pwsh
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

# This script verifies the setup of Azure components for E2E tests.

# Usage: start_azure_setup.ps1

function Verify-Secret(
    [string] $DaprNamespace,
    [string] $SecretName
) {
  begin{
    if([String]::IsNullOrWhiteSpace($DaprNamespace)){$DaprNamespace="default"}
  }
  process{
    $tries = 0
    $found = $false
    while(!$found) {
      kubectl describe secret $SecretName --namespace=$DaprNamespace
      $tries = $tries + 1
      if(!$?) {
          if($tries -ge 30) {
            throw "Did not find secret: $SecretName."
          }
          Write-Host "Did not find secret: $SecretName, retrying ..."
          Start-Sleep -s 10
      } else {
        $found = $true
      }
    }
  }
  end{}
}

Verify-Secret $env:DAPR_NAMESPACE cosmosdb-secret
Verify-Secret $env:DAPR_NAMESPACE servicebus-secret

Write-Host "Setup verified."
