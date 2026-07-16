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

@description('Prefix for all the resources')
param namePrefix string

@description('The location of the resources')
param location string = resourceGroup().location

param cosmosDbThroughput int = 1000

var databaseAccountName = '${namePrefix}db'

var cosmosDbServerlessCapabilities = {
  name: 'EnableServerless'
}
var cosmosDbThroughputObj = {
  throughput: cosmosDbThroughput
}

/* Cosmos DB Account */
resource databaseAccount 'Microsoft.DocumentDB/databaseAccounts@2021-04-15' = {
  name: databaseAccountName
  kind: 'GlobalDocumentDB'
  location: location
  properties: {
    consistencyPolicy: {
      defaultConsistencyLevel: 'Strong'
    }
    locations: [
      {
        locationName: location
      }
    ]
    capabilities: cosmosDbThroughput == 0 ? [
      cosmosDbServerlessCapabilities
    ] : []
    databaseAccountOfferType: 'Standard'
    enableAutomaticFailover: false
    enableMultipleWriteLocations: false
  }

  /* Database in Cosmos DB */
  resource database 'sqlDatabases@2021-04-15' = {
    name: 'dapre2e'
    properties: {
      resource: {
        id: 'dapre2e'
      }
      options: cosmosDbThroughput > 0 ? cosmosDbThroughputObj : {}
    }

    /* Container "items" */
    resource itemsContainer 'containers@2021-04-15' = {
      name: 'items'
      properties: {
        resource: {
          id: 'items'
          partitionKey: {
            paths: [
              '/partitionKey'
            ]
            kind: 'Hash'
          }
          defaultTtl: -1
        }
        options: cosmosDbThroughput > 0 ? cosmosDbThroughputObj : {}
      }
    }
  
    /* Container "items-query" */
    resource itemsQueryContainer 'containers@2021-04-15' = {
      name: 'items-query'
      properties: {
        resource: {
          id: 'items-query'
          partitionKey: {
            paths: [
              '/partitionKey'
            ]
            kind: 'Hash'
          }
        }
        options: cosmosDbThroughput > 0 ? cosmosDbThroughputObj : {}
      }
    }
  }

  /* RBAC role: Data Reader */
  resource dataReaderRole 'sqlRoleDefinitions@2021-10-15' = {
    name: '00000000-0000-0000-0000-000000000001'
    properties: {
      roleName: 'Cosmos DB Built-in Data Reader'
      type: 'BuiltInRole'
      assignableScopes: [
        databaseAccount.id
      ]
      permissions: [
        {
          dataActions: [
            'Microsoft.DocumentDB/databaseAccounts/readMetadata'
            'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/executeQuery'
            'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/readChangeFeed'
            'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/items/read'
          ]
        }
      ]
    }
  }

  /* RBAC role: Data Contributor */
  resource dataContributorRole 'sqlRoleDefinitions@2021-10-15' = {
    name: '00000000-0000-0000-0000-000000000002'
    properties: {
      roleName: 'Cosmos DB Built-in Data Contributor'
      type: 'BuiltInRole'
      assignableScopes: [
        databaseAccount.id
      ]
      permissions: [
        {
          dataActions: [
            'Microsoft.DocumentDB/databaseAccounts/readMetadata'
            'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/*'
            'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers/items/*'
          ]
        }
      ]
    }
  }
}

output accountId string = databaseAccount.id
output accountName string = databaseAccount.name
output dataReaderRoleId string = databaseAccount::dataReaderRole.id
output dataContributorRoleId string = databaseAccount::dataContributorRole.id
