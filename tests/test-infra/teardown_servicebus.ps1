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

# This script tears down Azure ServiceBus resources used by E2E tests.

# Usage: teardown_servicebus.ps1

function TearDownServiceBus(
  [string] $DaprNamespace,
  [string] $DaprTestResouceGroup,
  [string] $DaprTestServiceBusNamespace
) {
  begin{
    if([String]::IsNullOrWhiteSpace($DaprNamespace)){$DaprNamespace="default"}
    if([String]::IsNullOrWhiteSpace($DaprTestResouceGroup)){$DaprTestResouceGroup="dapre2e"}
    if([String]::IsNullOrWhiteSpace($DaprTestServiceBusNamespace)){$DaprTestServiceBusNamespace="dapr-aks-e2e-tests"}
  }
  process{
    Write-Host "Selected Kubernetes namespace: $DaprNamespace"
    Write-Host "Selected Dapr Test Resource group: $DaprTestResouceGroup"
    Write-Host "Selected Dapr Test ServiceBus namespace: $DaprTestServiceBusNamespace"

    # Reuse namespace since it can avoid connectivity issues on first call in the tests.
    Write-Host "Checking if servicebus namespace exists ..."
    $namespaceExistsResult = az servicebus namespace exists --name $DaprTestServiceBusNamespace | ConvertFrom-Json
    if(!$namespaceExistsResult.nameAvailable) {
      Write-Host "Servicebus namespace already exists. Updating to lower capacity ..."
      az servicebus namespace update --resource-group $DaprTestResouceGroup --name $DaprTestServiceBusNamespace --sku Premium --set sku.capacity=1
      if($?) {
        Write-Host "Updated servicebus namespace $DaprTestServiceBusNamespace."
      } else {
        Write-Host "Failed to update servicebus namespace $DaprTestServiceBusNamespace."
      }
    } else {
      Write-Host "Namespace does not exist. Nothing to be done."
    }

    Write-Host "ServiceBus tear down completed."
  }
  end{}
}

TearDownServiceBus $env:DAPR_NAMESPACE $env:DAPR_TEST_RESOURCE_GROUP $env:DAPR_TEST_SERVICEBUS_NAMESPACE
