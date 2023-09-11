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

@description('Name for the resources')
param name string

@description('Location of the resources')
param location string = resourceGroup().location

resource logAnalyticsWorkspace 'Microsoft.OperationalInsights/workspaces@2020-08-01' = {
  name: '${name}la'
  location: location
  properties: {
    sku: {
      name: 'PerGB2018'
    }
    retentionInDays: 30
    workspaceCapping: {
      dailyQuotaGb: 7
    }
  }
}

resource storageAccount 'Microsoft.Storage/storageAccounts@2021-08-01' = {
  name: '${name}sa'
  location: location
  sku: {
    name: 'Standard_LRS'
  }
  kind: 'StorageV2'
  properties: {
    accessTier: 'Hot'
  }
}

resource storageManagementPolicies 'Microsoft.Storage/storageAccounts/managementPolicies@2023-01-01' = {
  name: 'blobPolicy'
  parent: storageAccount
  properties: {
    policy: {
      rules: [
        {
          enabled: true
          name: 'Delete blob after 15 days'
          type: 'Lifecycle'
          definition: {
            actions: {
              baseBlob: {
                delete: {
                  daysAfterModificationGreaterThan: 15
                }
              }              
            }
          }
        }
      ]
    }
  }
}

output diagLogAnalyticsWorkspaceResourceId string = logAnalyticsWorkspace.id
output diagStorageResourceId string = storageAccount.id
