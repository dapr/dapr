#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

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
