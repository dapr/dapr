/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// This template deploys a test cluster (Linux or Windows) on Azure, and all associated resources

@minLength(3)
@description('Prefix for all the resources')
param namePrefix string

@description('The location of the resources')
param location string = resourceGroup().location

@description('If enabled, add a Windows pool')
param enableWindows bool = false

@description('If set, sends certain diagnostic logs to Log Analytics')
param diagLogAnalyticsWorkspaceResourceId string = ''

@description('If set, sends certain diagnostic logs to Azure Storage')
param diagStorageResourceId string = ''

@description('If enabled, deploy Cosmos DB')
param enableCosmosDB bool = true

@description('If enabled, deploy Service Bus')
param enableServiceBus bool = true

// Deploy an AKS cluster
module aksModule './azure-aks.bicep' = {
  name: 'azure-aks'
  params: {
    namePrefix: namePrefix
    location: location
    enableWindows: enableWindows
    diagLogAnalyticsWorkspaceResourceId: diagLogAnalyticsWorkspaceResourceId
    diagStorageResourceId: diagStorageResourceId
  }
}

// Deploy a Cosmos DB account
module cosmosdbModule './azure-cosmosdb.bicep' = if (enableCosmosDB) {
  name: 'azure-cosmosdb'
  params: {
    namePrefix: namePrefix
    location: location
  }
}

// Deploy a Service Bus namespace and all the topics/subscriptions
module serviceBusModule './azure-servicebus.bicep' = if (enableServiceBus) {
  name: 'azure-servicebus'
  params: {
    namePrefix: namePrefix
    location: location
  }
}

// This is temporarily turned off while we fix issues with Cosmos DB and RBAC
// See: https://github.com/dapr/components-contrib/issues/1603
/*
// Deploy RBAC roles to allow the AKS cluster to access resources in the Cosmos DB account
module cosmosdbRbacModule './azure-cosmosdb-rbac.bicep' = {
  name: 'azure-cosmosdb-rbac'
  params: {
    databaseAccountName: cosmosdbModule.outputs.accountName
    databaseRoleId: cosmosdbModule.outputs.dataContributorRoleId
    principalId: aksModule.outputs.aksManagedIdentityPrincipalId
    scope: cosmosdbModule.outputs.accountId
  }
}

// Outputs
output aksIdentityClientId string = aksModule.outputs.aksManagedIdentityClientId
output aksIdentityPrincipalId string = aksModule.outputs.aksManagedIdentityPrincipalId
*/
