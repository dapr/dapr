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

@description('The name of the Service Bus namespace')
param namespace string

@description('Name of the topic to create')
param topicName string

@description('Array with the name of the subscriptions to create')
param topicSubscriptions array

resource topic 'Microsoft.ServiceBus/namespaces/topics@2021-11-01' = {
  name: '${namespace}/${topicName}'
  properties: {
    maxMessageSizeInKilobytes: 1024
    maxSizeInMegabytes: 1024
  }

  resource subscription 'subscriptions@2021-11-01' = [for sub in topicSubscriptions: {
    name: sub
    properties: {
      lockDuration: 'PT5S'
      maxDeliveryCount: 999
      enableBatchedOperations: false
    }
  }]
}
