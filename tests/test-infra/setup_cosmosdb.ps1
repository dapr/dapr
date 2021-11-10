#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script sets up CosmosDB for E2E tests.

# Usage: setup_cosmosdb.ps1
#
# We use PowerShell script because Bash in MinGW messes up with the `partition-key-path` param.

function Setup-CosmosDB(
  [string] $DaprNamespace,
  [string] $DaprTestResouceGroup,
  [string] $DaprTestCosmosDBAccount,
  [string] $DaprTestCosmosDBDatabase,
  [string] $DaprTestCosmosDBCollection
) {
  begin{
    if([String]::IsNullOrWhiteSpace($DaprNamespace)){$DaprNamespace="default"}
    if([String]::IsNullOrWhiteSpace($DaprTestResouceGroup)){$DaprTestResouceGroup="dapre2e"}
    if([String]::IsNullOrWhiteSpace($DaprTestCosmosDBAccount)){$DaprTestCosmosDBAccount="dapr-e2e-tests"}
    if([String]::IsNullOrWhiteSpace($DaprTestCosmosDBDatabase)){$DaprTestCosmosDBDatabase="dapr-aks-e2e-tests"}
    if([String]::IsNullOrWhiteSpace($DaprTestCosmosDBCollection)){$DaprTestCosmosDBCollection="dapr-aks-e2e-state"}
  }
  process{
    Write-Host "Selected Kubernetes namespace: $DaprNamespace"
    Write-Host "Selected Dapr Test Resource group: $DaprTestResouceGroup"
    Write-Host "Selected Dapr Test CosmosDB account: $DaprTestCosmosDBAccount"
    Write-Host "Selected Dapr Test CosmosDB database: $DaprTestCosmosDBDatabase"
    Write-Host "Selected Dapr Test CosmosDB collection: $DaprTestCosmosDBCollection"

    # Delete fist since this secret is the criteria to determine if the setup completed.
    Write-Host "Deleting secret from Kubernetes cluster ..."
    kubectl delete secret cosmosdb-secret --namespace=$DaprNamespace
    if($?) {
      Write-Host "Deleted existing secret."
    } else {
      Write-Host "Failed to delete secret, skipping."
    }

    Write-Host "Deleting existing collection ..."
    az cosmosdb sql container delete -a $DaprTestCosmosDBAccount -g $DaprTestResouceGroup -n $DaprTestCosmosDBCollection -d $DaprTestCosmosDBDatabase --yes
    if($?) {
      Write-Host "Deleted existing collection."
    } else {
      Write-Host "Failed to delete collection, skipping."
    }

    Write-Host "Creating collection ..."
    az cosmosdb sql container create -g $DaprTestResouceGroup -a $DaprTestCosmosDBAccount -d $DaprTestCosmosDBDatabase -n $DaprTestCosmosDBCollection --partition-key-path '/partitionKey'
    if($?) {
      Write-Host "Created collection."
    } else {
      throw "Failed to create collection."
    }

    $primaryKey = az cosmosdb keys list --name $DaprTestCosmosDBAccount --resource-group $DaprTestResouceGroup | jq .primaryMasterKey
    kubectl create secret generic cosmosdb-secret --namespace=$DaprNamespace `
      --from-literal=url=https://$DaprTestCosmosDBAccount.documents.azure.com:443/ `
      --from-literal=collection=$DaprTestCosmosDBCollection `
      --from-literal=primaryMasterKey=$primaryKey
    if(!$?) {
      throw "Could not create cosmosdb-secret."
    }

    Write-Host "Setup completed."
  }
  end{}
}

Setup-CosmosDB $env:DAPR_NAMESPACE $env:DAPR_TEST_RESOURCE_GROUP $env:DAPR_TEST_COSMOSDB_ACCOUNT $env:DAPR_TEST_COSMOSDB_DATABASE $env:DAPR_TEST_COSMOSDB_COLLECTION