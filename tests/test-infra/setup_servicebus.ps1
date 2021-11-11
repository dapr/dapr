#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script sets up Service Bus for E2E tests.

# Usage: setup_servicebus.ps1

$TOPICS_AND_SUBSCRIPTIONS = `
  [pscustomobject]@{
    topics = "pubsub-a-topic-http", "pubsub-b-topic-http", "pubsub-c-topic-http", "pubsub-raw-topic-http";
    subscriptions = "pubsub-publisher", "pubsub-subscriber", "pubsub-publisher-grpc", "pubsub-subscriber-grpc";
  }, `
  [pscustomobject]@{
    topics = "pubsub-a-topic-grpc", "pubsub-b-topic-grpc", "pubsub-c-topic-grpc", "pubsub-raw-topic-grpc";
    subscriptions = "pubsub-publisher", "pubsub-subscriber", "pubsub-publisher-grpc", "pubsub-subscriber-grpc";
  }, `
  [pscustomobject]@{
    topics = "pubsub-routing-http", "pubsub-routing-crd-http";
    subscriptions = "pubsub-publisher-routing", "pubsub-subscriber-routing";
  }, `
  [pscustomobject]@{
    topics = "pubsub-routing-grpc", "pubsub-routing-crd-grpc";
    subscriptions = "pubsub-publisher-routing-grpc", "pubsub-subscriber-routing-grpc";
  },
  [pscustomobject]@{
    topics = ,"pubsub-job-topic-http";
    subscriptions = "pubsub-publisher", "pubsub-subscriber", "job-subscriber", "pubsub-publisher-grpc", "pubsub-subscriber-grpc";
  },
  [pscustomobject]@{
    topics = ,"pubsub-healthcheck-topic-http";
    subscriptions = "pubsub-publisher", "pubsub-subscriber", "pubsub-publisher-grpc", "pubsub-subscriber-grpc";
  },
  [pscustomobject]@{
    topics = ,"pubsub-healthcheck-topic-grpc";
    subscriptions = "pubsub-publisher", "pubsub-subscriber", "pubsub-publisher-grpc", "pubsub-subscriber-grpc";
  }

function Setup-ServiceBus-Subscription(
  [string] $DaprTestResouceGroup,
  [string] $DaprTestServiceBusNamespace,
  [string] $DaprTestServiceBusTopic,
  [string] $DaprTestServiceBusSubscription
) {
  Write-Host "Creating servicebus subscription $DaprTestServiceBusSubscription in topic $DaprTestServiceBusTopic ..."
  az servicebus topic subscription create --resource-group $DaprTestResouceGroup --namespace-name $DaprTestServiceBusNamespace --topic-name $DaprTestServiceBusTopic `
    --name $DaprTestServiceBusSubscription `
    --lock-duration '0:0:5' `
    --max-delivery-count 999 `
    --enable-batched-operations false
  if($?) {
    Write-Host "Created servicebus subscription $DaprTestServiceBusSubscription in topic $DaprTestServiceBusTopic."
  } else {
    throw "Failed to create servicebus subscription $DaprTestServiceBusSubscription in topic $DaprTestServiceBusTopic."
  }
}


function Setup-ServiceBus-Topic(
  [string] $DaprTestResouceGroup,
  [string] $DaprTestServiceBusNamespace,
  [string] $DaprTestServiceBusTopic
) {
  Write-Host "Deleting servicebus topic $DaprTestServiceBusTopic ..."
  az servicebus topic delete --resource-group $DaprTestResouceGroup --namespace-name $DaprTestServiceBusNamespace --name $DaprTestServiceBusTopic
  if($?) {
    Write-Host "Deleted servicebus topic $DaprTestServiceBusTopic."
  } else {
    Write-Host "Failed to delete servicebus topic $DaprTestServiceBusTopic, skipping."
  }

  Write-Host "Creating servicebus topic $DaprTestServiceBusTopic ..."
  az servicebus topic create --resource-group $DaprTestResouceGroup --namespace-name $DaprTestServiceBusNamespace --name $DaprTestServiceBusTopic
  if($?) {
    Write-Host "Created servicebus topic $DaprTestServiceBusTopic."
  } else {
    throw "Failed to create servicebus topic $DaprTestServiceBusTopic."
  }
}

function Setup-ServiceBus-Topics-And-Subscriptions(
  [string] $DaprTestResouceGroup,
  [string] $DaprTestServiceBusNamespace,
  [string[]] $DaprTestServiceBusTopics,
  [string[]] $DaprTestServiceBusSubscriptions
) {
  foreach ($topic in $DaprTestServiceBusTopics) {
    Setup-ServiceBus-Topic $DaprTestResouceGroup $DaprTestServiceBusNamespace $topic

    foreach ($subscription in $DaprTestServiceBusSubscriptions) {
      Setup-ServiceBus-Subscription $DaprTestResouceGroup $DaprTestServiceBusNamespace $topic $subscription
    }
  }
}

function Setup-ServiceBus(
  [string] $DaprNamespace,
  [string] $DaprTestResouceGroup,
  [string] $DaprTestServiceBusNamespace
) {
  begin{
    if([String]::IsNullOrWhiteSpace($DaprNamespace)){$DaprNamespace="default"}
    if([String]::IsNullOrWhiteSpace($DaprTestResouceGroup)){$DaprTestResouceGroup="dapre2e"}
    if([String]::IsNullOrWhiteSpace($DaprTestServiceBusNamespace)){$DaprTestServiceBusNamespace="dapr-aks-e2e-tests"}
  }
  process{
    Write-Host "Selected Kubernetes namespace: $DaprNamespace"
    Write-Host "Selected Dapr Test Resource group: $DaprTestResouceGroup"
    Write-Host "Selected Dapr Test ServiceBus namespace: $DaprTestServiceBusNamespace"

    # Delete fist since this secret is the criteria to determine if the setup completed.
    Write-Host "Deleting secret from Kubernetes cluster ..."
    kubectl delete secret servicebus-secret --namespace=$DaprNamespace
    if($?) {
      Write-Host "Deleted existing secret."
    } else {
      Write-Host "Failed to delete secret, skipping."
    }

    # Reuse namespace (when possible) since it can avoid connectivity issues on first call in the tests.
    Write-Host "Checking if servicebus namespace exists ..."
    $serviceBusNamespaceDetails = az servicebus namespace show --resource-group $DaprTestResouceGroup --name $DaprTestServiceBusNamespace | ConvertFrom-Json
    $exists = $?
    if($exists -and ($serviceBusNamespaceDetails.sku.tier -ne 'Premium')) {
      # Namespace exists but is not premium, deleting it.
      Write-Host "Deleting servicebus namespace ..."
      az servicebus namespace delete --resource-group $DaprTestResouceGroup --name $DaprTestServiceBusNamespace
      if($?) {
        Write-Host "Deleted servicebus namespace."
      } else {
        throw "Failed to delete servicebus namespace."
      }
      $exists = $false
    }

    if(!$exists) {
      Write-Host "Creating servicebus namespace ..."
      az servicebus namespace create --resource-group $DaprTestResouceGroup --name $DaprTestServiceBusNamespace --location westus2 --sku Premium
      if($?) {
        Write-Host "Created servicebus namespace."
      } else {
        throw "Failed to create servicebus namespace."
      }
    }

    Write-Host "Updating servicebus namespace ..."
    az servicebus namespace update --resource-group $DaprTestResouceGroup --name $DaprTestServiceBusNamespace --sku Premium --set sku.capacity=16
    if($?) {
      Write-Host "Updated servicebus namespace."
    } else {
      throw "Failed to update servicebus namespace."
    }

    foreach ($topicsAndSubscriptions in $TOPICS_AND_SUBSCRIPTIONS) {
      Setup-ServiceBus-Topics-And-Subscriptions $DaprTestResouceGroup $DaprTestServiceBusNamespace $topicsAndSubscriptions.topics $topicsAndSubscriptions.subscriptions
    }

    $connectionString = az servicebus namespace authorization-rule keys list --resource-group $DaprTestResouceGroup --namespace-name $DaprTestServiceBusNamespace --name RootManageSharedAccessKey --query primaryConnectionString --output tsv
    kubectl create secret generic servicebus-secret --namespace=$DaprNamespace `
      --from-literal=connectionString="$connectionString"
    if(!$?) {
      throw "Could not create servicebus-secret."
    }

    Write-Host "Setup completed."
  }
  end{}
}

Setup-ServiceBus $env:DAPR_NAMESPACE $env:DAPR_TEST_RESOURCE_GROUP $env:DAPR_TEST_SERVICEBUS_NAMESPACE
