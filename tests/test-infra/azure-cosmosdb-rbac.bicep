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

@description('Name of the Cosmos DB database account resource')
param databaseAccountName string

@description('ID of the role to assign')
param databaseRoleId string

@description('ID of the principal')
param principalId string

@description('ID of the scope (database account or database or collection)')
param scope string

resource rbacAssignment 'Microsoft.DocumentDB/databaseAccounts/sqlRoleAssignments@2021-04-01-preview' = {
  name: '${databaseAccountName}/${guid(databaseAccountName, databaseRoleId, principalId)}'
  properties: {
    roleDefinitionId: databaseRoleId
    principalId: principalId
    scope: scope
  }
}
