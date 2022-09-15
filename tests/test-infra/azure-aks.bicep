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

@description('If enabled, add a Windows pool')
param enableWindows bool = false

@description('VM size to use for Linux nodes (agent pool)')
param linuxVMSize string = 'Standard_DS2_v2'

@description('VM size to use for Windows nodes, if enabled')
param windowsVMSize string = 'Standard_DS3_v2'

@description('If set, sends certain diagnostic logs to Log Analytics')
param diagLogAnalyticsWorkspaceResourceId string = ''

@description('If set, sends certain diagnostic logs to Azure Storage')
param diagStorageResourceId string = ''

// Disk size (in GB) for each of the agent pool nodes
// 0 applies the default
var osDiskSizeGB = 0

// Version of Kubernetes
var kubernetesVersion = '1.22.6'

resource containerRegistry 'Microsoft.ContainerRegistry/registries@2019-05-01' = {
  name: '${namePrefix}acr'
  location: location
  sku: {
    name: 'Standard'
  }
  properties: {
    adminUserEnabled: true
  }
  tags: {}
}

resource roleAssignContainerRegistry 'Microsoft.Authorization/roleAssignments@2020-04-01-preview' = {
  name: guid(containerRegistry.id, '${namePrefix}-aks', 'b24988ac-6180-42a0-ab88-20f7382dd24c')
  properties: {
    roleDefinitionId: '/subscriptions/${subscription().subscriptionId}/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c'
    principalId: reference('${namePrefix}-aks', '2021-07-01').identityProfile.kubeletidentity.objectId
  }
  scope: containerRegistry
  dependsOn:[
    aks
  ]
}

// Network profile when the cluster has Windows nodes
var networkProfileWindows = {
  networkPlugin: 'azure'
  serviceCidr: '10.0.0.0/16'
  dnsServiceIP: '10.0.0.10'
  dockerBridgeCidr: '172.17.0.1/16'
}

// Network profile when the cluster has only Linux nodes
var networkProfileLinux = {
  networkPlugin: 'kubenet'
}

resource aks 'Microsoft.ContainerService/managedClusters@2021-07-01' = {
  location: location
  name: '${namePrefix}-aks'
  properties: {
    kubernetesVersion: kubernetesVersion
    enableRBAC: true
    dnsPrefix: '${namePrefix}-dns'
    agentPoolProfiles: concat([
        {
          name: 'agentpool'
          osDiskSizeGB: osDiskSizeGB
          enableAutoScaling: false
          count: 3
          vmSize: linuxVMSize
          osType: 'Linux'
          type: 'VirtualMachineScaleSets'
          mode: 'System'
          maxPods: 110
          availabilityZones: [
            '1'
            '2'
            '3'
          ]
          enableNodePublicIP: false
          vnetSubnetID: enableWindows ? aksVNet::defaultSubnet.id : null
          tags: {}
        }
      ], enableWindows ? [
        {
          name: 'winpol'
          osDiskSizeGB: osDiskSizeGB
          osDiskType: 'Ephemeral'
          enableAutoScaling: false
          count: 2
          vmSize: windowsVMSize
          osType: 'Windows'
          type: 'VirtualMachineScaleSets'
          mode: 'User'
          maxPods: 110
          availabilityZones: [
            '1'
            '2'
            '3'
          ]
          nodeLabels: {}
          nodeTaints: []
          enableNodePublicIP: false
          vnetSubnetID: aksVNet::defaultSubnet.id
          tags: {}
        }
      ] : [])
    networkProfile: union({
        loadBalancerSku: 'standard'
      }, enableWindows ? networkProfileWindows : networkProfileLinux)
    apiServerAccessProfile: {
      enablePrivateCluster: false
    }
    addonProfiles: {
      httpApplicationRouting: {
        enabled: true
      }
      azurepolicy: {
        enabled: false
      }
      azureKeyvaultSecretsProvider: {
        enabled: false
      }
      omsagent: diagLogAnalyticsWorkspaceResourceId == '' ? {
        enabled: false
      } : {
        enabled: true
        config: {
          logAnalyticsWorkspaceResourceID: diagLogAnalyticsWorkspaceResourceId
        }
      }
    }
  }
  tags: {}
  sku: {
    name: 'Basic'
    tier: 'Paid'
  }
  identity: {
    type: 'SystemAssigned'
  }
}

resource aksVNet 'Microsoft.Network/virtualNetworks@2020-11-01' = if (enableWindows) {
  location: location
  name: '${namePrefix}-vnet'
  properties: {
    subnets: [
      {
        name: 'default'
        properties: {
          addressPrefix: '10.240.0.0/16'
        }
      }
    ]
    addressSpace: {
      addressPrefixes: [
        '10.0.0.0/8'
      ]
    }
  }
  tags: {}

  resource defaultSubnet 'subnets' existing = {
    name: 'default'
  }
}

resource roleAssignVNet 'Microsoft.Authorization/roleAssignments@2020-04-01-preview' = if (enableWindows) {
  name: guid('${aksVNet.id}/subnets/default', '${namePrefix}-vnet', 'b24988ac-6180-42a0-ab88-20f7382dd24c')
  properties: {
    roleDefinitionId: '/subscriptions/${subscription().subscriptionId}/providers/Microsoft.Authorization/roleDefinitions/4d97b98b-1d4f-4787-a291-c67834d212e7'
    principalId: aks.identity.principalId
  }
  scope: aksVNet::defaultSubnet
}

resource aksDiagnosticLogAnalytics 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (diagLogAnalyticsWorkspaceResourceId != '') {
  name: 'loganalytics'
  scope: aks
  properties: {
    logs: [
      {
        category: 'kube-apiserver'
        enabled: true
      }
      {
        category: 'kube-controller-manager'
        enabled: true
      }
    ]
    workspaceId: diagLogAnalyticsWorkspaceResourceId
  }
}

resource aksDiagnosticStorage 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (diagStorageResourceId != '') {
  name: 'storage'
  scope: aks
  properties: {
    logs: [
      {
        category: 'kube-apiserver'
        enabled: true
        retentionPolicy: {
          days: 15
          enabled: true
        }
      }
      {
        category: 'kube-audit'
        enabled: true
        retentionPolicy: {
          days: 15
          enabled: true
        }
      }
    ]
    storageAccountId: diagStorageResourceId
  }
}

output controlPlaneFQDN string = aks.properties.fqdn
output aksManagedIdentityClientId string = aks.properties.identityProfile.kubeletidentity.clientId
output aksManagedIdentityPrincipalId string = aks.properties.identityProfile.kubeletidentity.objectId
