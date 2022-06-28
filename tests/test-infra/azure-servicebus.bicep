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

var topicsAndSubscriptions = [
  {
    name: 'pubsub-a-topic-http'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-b-topic-http'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-c-topic-http'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-raw-topic-http'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-a-topic-grpc'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-b-topic-grpc'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-c-topic-grpc'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-raw-topic-grpc'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-routing-http'
    subscriptions: [
      'pubsub-publisher-routing'
      'pubsub-subscriber-routing'
    ]
  }
  {
    name: 'pubsub-routing-crd-http'
    subscriptions: [
      'pubsub-publisher-routing'
      'pubsub-subscriber-routing'
    ]
  }
  {
    name: 'pubsub-routing-grpc'
    subscriptions: [
      'pubsub-publisher-routing-grpc'
      'pubsub-subscriber-routing-grpc'
    ]
  }
  {
    name: 'pubsub-routing-crd-grpc'
    subscriptions: [
      'pubsub-publisher-routing-grpc'
      'pubsub-subscriber-routing-grpc'
    ]
  }
  {
    name: 'pubsub-job-topic-http'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'job-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-healthcheck-topic-http'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
  {
    name: 'pubsub-healthcheck-topic-grpc'
    subscriptions: [
      'pubsub-publisher'
      'pubsub-subscriber'
      'pubsub-publisher-grpc'
      'pubsub-subscriber-grpc'
    ]
  }
]

resource serviceBusNamespace 'Microsoft.ServiceBus/namespaces@2021-11-01' = {
  name: '${namePrefix}sb'
  location: location
  sku: {
    name: 'Premium'
    tier: 'Premium'
    capacity: 4
  }
  properties: {
    disableLocalAuth: false
    zoneRedundant: false
  }
}

module topicSubscriptions './azure-servicebus-topic.bicep' = [for topicSub in topicsAndSubscriptions: {
  name: '${topicSub.name}'
  params: {
    namespace: serviceBusNamespace.name
    topicName: topicSub.name
    topicSubscriptions: topicSub.subscriptions
  }
  dependsOn: [
    //serviceBusNamespace
  ]
}]
