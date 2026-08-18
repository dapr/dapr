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

# Control if we're using Cosmos DB, Service Bus, and Azure Key Vault
USE_COSMOSDB=${1:-true}
USE_SERVICEBUS=${2:-true}
USE_KEYVAULT=${3:-true}

if $USE_COSMOSDB ; then
  echo "Configuring Cosmos DB as state store"

  # Set environmental variables to use Cosmos DB as state store
  export DAPR_TEST_STATE_STORE=cosmosdb
  export DAPR_TEST_QUERY_STATE_STORE=cosmosdb

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
  echo "Configuring Service Bus as pubsub broker"

  # Set environmental variables to Service Bus pubsub
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
  echo "NOT configuring Service Bus as pubsub broker"
fi

if $USE_KEYVAULT ; then
  echo "Configuring Azure Key Vault as crypto provider"

  # Set environmental variables to Azure Key Vault crypto provider
  export DAPR_TEST_CRYPTO=azurekeyvault
  if [ -n "$GITHUB_ENV" ]; then
    echo "DAPR_TEST_CRYPTO=${DAPR_TEST_CRYPTO}" >> $GITHUB_ENV
  fi

  AzureKeyVaultTenantId=$(echo $AZURE_CREDENTIALS | jq -r '.tenantId')
  AzureKeyVaultClientId=$(echo $AZURE_CREDENTIALS | jq -r '.clientId')
  AzureKeyVaultClientSecret=$(echo $AZURE_CREDENTIALS | jq -r '.clientSecret')

  kubectl create secret generic azurekeyvault-secret \
    --namespace=$DAPR_NAMESPACE \
    --from-literal="tenant-id=${AzureKeyVaultTenantId}" \
    --from-literal="client-id=${AzureKeyVaultClientId}" \
    --from-literal="client-secret=${AzureKeyVaultClientSecret}"

  # Ensure the test RSA key 'rsakey' exists in the configured Key Vault.
  # The crypto E2E tests reference this key by name (see
  # tests/e2e/crypto/crypto_test.go). Creating it here is idempotent: az
  # keyvault key show returns 0 if the key already exists, otherwise we
  # create it.
  if [[ -z "$AZURE_KEY_VAULT_NAME" ]]; then
    echo "Skipping rsakey bootstrap: AZURE_KEY_VAULT_NAME is not set"
  else
    if az keyvault key show \
        --vault-name "$AZURE_KEY_VAULT_NAME" \
        --name rsakey \
        --output none 2>/dev/null ; then
      echo "rsakey already exists in vault $AZURE_KEY_VAULT_NAME"
    else
      echo "Creating rsakey in vault $AZURE_KEY_VAULT_NAME"
      az keyvault key create \
        --vault-name "$AZURE_KEY_VAULT_NAME" \
        --name rsakey \
        --kty RSA \
        --size 2048 \
        --output none
    fi
  fi
else
  echo "NOT configuring Azure Key Vault as crypto provider"
fi
