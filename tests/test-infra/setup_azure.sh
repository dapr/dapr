#!/usr/bin/env bash

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

if [[ -z "$TEST_PREFIX" ]]; then
    echo "Environmental variable TEST_PREFIX must be set" 1>&2
    exit 1
fi
if [[ -z "$TEST_RESOURCE_GROUP" ]]; then
    echo "Environmental variable TEST_RESOURCE_GROUP must be set" 1>&2
    exit 1
fi

# Control if we're using Cosmos DB
USE_COSMOSDB=${1:-true}
USE_SERVICEBUS=${2:-true}
USE_RABBITMQ=${3:-true}

if $USE_COSMOSDB ; then
  echo "Configuring Cosmos DB as state store"

  # Set environmental variables to use Cosmos DB as state store
  export DAPR_TEST_STATE_STORE=cosmosdb
  export DAPR_TEST_QUERY_STATE_STORE=cosmosdb_query

  # Write into the GitHub Actions environment
  if [ -n "$GITHUB_ENV" ]; then
    echo "DAPR_TEST_STATE_STORE=${DAPR_TEST_STATE_STORE}" >> $GITHUB_ENV
    echo "DAPR_TEST_QUERY_STATE_STORE=${DAPR_TEST_QUERY_STATE_STORE}" >> $GITHUB_ENV
  fi

  # Get the credentials for Cosmos DB
  COSMOSDB_MASTER_KEY=$(
    az cosmosdb keys list \
      --name "${TEST_PREFIX}db" \
      --resource-group "$TEST_RESOURCE_GROUP" \
      --query "primaryMasterKey" \
      -o tsv
  )
  kubectl create secret generic cosmosdb-secret \
    --namespace=$DAPR_NAMESPACE \
    --from-literal=url=https://${TEST_PREFIX}db.documents.azure.com:443/ \
    --from-literal=primaryMasterKey=${COSMOSDB_MASTER_KEY}
else
  echo "NOT configuring Cosmos DB as state store"
fi

if $USE_SERVICEBUS ; then
  echo "Configuring Service Bus as pubsub store"

  # Set environmental variables to Service Bus state store
  export DAPR_TEST_PUBSUB=servicebus
  if [ -n "$GITHUB_ENV" ]; then
    echo "DAPR_TEST_PUBSUB=${DAPR_TEST_PUBSUB}" >> $GITHUB_ENV
  fi

  # Get the credentials for Service Bus
  SERVICEBUS_CONNSTRING=$(
    az servicebus namespace authorization-rule keys list \
      --resource-group "$TEST_RESOURCE_GROUP" \
      --namespace-name "${TEST_PREFIX}sb" \
      --name RootManageSharedAccessKey \
      --query primaryConnectionString \
      -o tsv
  )
  kubectl create secret generic servicebus-secret \
    --namespace=$DAPR_NAMESPACE \
    --from-literal=connectionString=${SERVICEBUS_CONNSTRING}
else
  echo "NOT configuring Service Bus as pubsub store"
fi

if $USE_RABBITMQ ; then
  echo "Configuring RabbitMQ as pubsub component"

  # Get the credentials for RabbitMQ
  RABBITMQ_PWD=$(kubectl get secret --namespace dapr-tests rabbitmq -o jsonpath="{.data.rabbitmq-password}" | base64 -d)

  kubectl create secret generic rabbitmq-secret \
    --namespace=$DAPR_NAMESPACE \
    --from-literal=connectionString="amqp://user:${RABBITMQ_PWD}@rabbitmq.dapr-tests.svc.cluster.local:5672"
else
  echo "NOT configuring RabbitMQ"
fi
