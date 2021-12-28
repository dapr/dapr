#!/usr/bin/env pwsh
#
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

# This script sets up CosmosDB for E2E tests.

# Usage: setup_cosmosdb.ps1
#
# We use PowerShell script because Bash in MinGW messes up with the `partition-key-path` param.

function Setup-CosmosDB(
  [string] $DaprNamespace,
  [string] $DaprTestResouceGroup,
  [string] $DaprTestCosmosDBAccount,
  [string] $DaprTestCosmosDBDatabase,
  [string] $DaprTestCosmosDBCollection,
  [string] $DaprTestCosmosDBQueryCollection
) {
  begin{
    if([String]::IsNullOrWhiteSpace($DaprNamespace)){$DaprNamespace="default"}
    if([String]::IsNullOrWhiteSpace($DaprTestResouceGroup)){$DaprTestResouceGroup="dapre2e"}
    if([String]::IsNullOrWhiteSpace($DaprTestCosmosDBAccount)){$DaprTestCosmosDBAccount="dapr-e2e-tests"}
    if([String]::IsNullOrWhiteSpace($DaprTestCosmosDBDatabase)){$DaprTestCosmosDBDatabase="dapr-aks-e2e-tests"}
    if([String]::IsNullOrWhiteSpace($DaprTestCosmosDBCollection)){$DaprTestCosmosDBCollection="dapr-aks-e2e-state"}
    if([String]::IsNullOrWhiteSpace($DaprTestCosmosDBQueryCollection)){$DaprTestCosmosDBQueryCollection="dapr-aks-e2e-state-query"}
  }
  process{
    Write-Host "Selected Kubernetes namespace: $DaprNamespace"
    Write-Host "Selected Dapr Test Resource group: $DaprTestResouceGroup"
    Write-Host "Selected Dapr Test CosmosDB account: $DaprTestCosmosDBAccount"
    Write-Host "Selected Dapr Test CosmosDB database: $DaprTestCosmosDBDatabase"
    Write-Host "Selected Dapr Test CosmosDB collection: $DaprTestCosmosDBCollection"
    Write-Host "Selected Dapr Test CosmosDB query collection: $DaprTestCosmosDBQueryCollection"

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

    Write-Host "Deleting existing query collection ..."
    az cosmosdb sql container delete -a $DaprTestCosmosDBAccount -g $DaprTestResouceGroup -n $DaprTestCosmosDBQueryCollection -d $DaprTestCosmosDBDatabase --yes
    if($?) {
      Write-Host "Deleted existing query collection."
    } else {
      Write-Host "Failed to delete query collection, skipping."
    }

    Write-Host "Creating query collection ..."
    az cosmosdb sql container create -g $DaprTestResouceGroup -a $DaprTestCosmosDBAccount -d $DaprTestCosmosDBDatabase -n $DaprTestCosmosDBQueryCollection --partition-key-path '/partitionKey'
    if($?) {
      Write-Host "Created query collection."
    } else {
      throw "Failed to create query collection."
    }

    $primaryKey = az cosmosdb keys list --name $DaprTestCosmosDBAccount --resource-group $DaprTestResouceGroup | jq .primaryMasterKey
    kubectl create secret generic cosmosdb-secret --namespace=$DaprNamespace `
      --from-literal=url=https://$DaprTestCosmosDBAccount.documents.azure.com:443/ `
      --from-literal=collection=$DaprTestCosmosDBCollection `
      --from-literal=queryCollection=$DaprTestCosmosDBQueryCollection `
      --from-literal=primaryMasterKey=$primaryKey
    if(!$?) {
      throw "Could not create cosmosdb-secret."
    }

    Write-Host "Setup completed."
  }
  end{}
}

Setup-CosmosDB $env:DAPR_NAMESPACE $env:DAPR_TEST_RESOURCE_GROUP $env:DAPR_TEST_COSMOSDB_ACCOUNT $env:DAPR_TEST_COSMOSDB_DATABASE $env:DAPR_TEST_COSMOSDB_COLLECTION $env:DAPR_TEST_COSMOSDB_QUERY_COLLECTION
