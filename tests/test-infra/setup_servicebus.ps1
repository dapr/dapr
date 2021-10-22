#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script sets up Service Bus for E2E tests.

# Usage: setup_servicebus.ps1

function Setup-ServiceBus(
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

    Write-Host "Deleting existing servicebus namespace ..."
    az servicebus namespace delete --resource-group $DaprTestResouceGroup --name $DaprTestServiceBusNamespace
    if($?) {
      Write-Host "Deleted existing servicebus namespace."
    } else {
      Write-Host "Failed to delete servicebus namespace, skipping."
    }

    Write-Host "Creating servicebus namespace ..."
    az servicebus namespace create --resource-group $DaprTestResouceGroup --name $DaprTestServiceBusNamespace --location westus2
    if($?) {
      Write-Host "Created servicebus namespace."
    } else {
      throw "Failed to create servicebus namespace."
    }


    Write-Host "Deleting secret from Kubernetes cluster ..."
    kubectl delete secret servicebus-secret --namespace=$DaprNamespace
    if($?) {
      Write-Host "Deleted existing secret."
    } else {
      Write-Host "Failed to delete secret, skipping."
    }

    $connectionString = az servicebus namespace authorization-rule keys list --resource-group $DaprTestResouceGroup --namespace-name $DaprTestServiceBusNamespace --name RootManageSharedAccessKey --query primaryConnectionString --output tsv
    kubectl create secret generic servicebus-secret --namespace=$DaprNamespace `
      --from-literal=connectionString="$connectionString"
    if(!$?) {
      throw "Could not create servicebus-secret."
    }

    Write-Host "Setup completed."
  }
  end{}
}

Setup-ServiceBus $env:DAPR_NAMESPACE $env:DAPR_TEST_RESOURCE_GROUP $env:DAPR_TEST_SERVICEBUS_NAMESPACE