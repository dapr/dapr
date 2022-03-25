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

// This template deploys both the Linux and Windows test clusters in Azure
// It needs to be deployed at a subscription level

targetScope = 'subscription'

@minLength(3)
@description('Prefix for all the resources')
param namePrefix string

@description('The location of the first set of resources')
param location1 string

@description('The location of the second set of resources')
param location2 string

// Deploy the Linux cluster in the first location
resource linuxResources 'Microsoft.Resources/resourceGroups@2020-10-01' = {
  name: 'Dapr-E2E-${namePrefix}l'
  location: location1
}
module linuxCluster 'azure.bicep' = {
  name: 'linuxCluster'
  scope: linuxResources
  params: {
    namePrefix: '${namePrefix}l'
    location: location1
    enableWindows: false
  }
}

// Deploy the Windows cluster in the second location
resource WindowsResources 'Microsoft.Resources/resourceGroups@2020-10-01' = {
  name: 'Dapr-E2E-${namePrefix}w'
  location: location2
}
module windowsCluster 'azure.bicep' = {
  name: 'windowsCluster'
  scope: WindowsResources
  params: {
    namePrefix: '${namePrefix}w'
    location: location2
    enableWindows: true
  }
}
