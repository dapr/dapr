#!/usr/bin/env bash

# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script sets up CosmosDB for E2E tests.

# Usage: setup_cosmosdb.sh

if [ $TARGET_OS == "windows" ]; then
  # This is needed because bash on Windows (MinGW) messes up with the `partition-key-path` param by prefixing it with a Windows file path.
  # So, we rewrote this script in PowerShell to avoid this problem on Windows.
  # TODO(artursouza): delete this script and use PowerShell script only for Linux & Windows.
  SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
  echo "Current script dir: $SCRIPT_DIR"
  pwsh "$SCRIPT_DIR/setup_cosmosdb.ps1"
  exit $?
fi

[ -z "$DAPR_NAMESPACE" ] && DAPR_NAMESPACE="default"

[ -z "$DAPR_TEST_RESOURCE_GROUP" ] && DAPR_TEST_RESOURCE_GROUP="dapre2e"

[ -z "$DAPR_TEST_COSMOSDB_ACCOUNT" ] && DAPR_TEST_COSMOSDB_ACCOUNT="dapr-e2e-tests"

[ -z "$DAPR_TEST_COSMOSDB_DATABASE" ] && DAPR_TEST_COSMOSDB_DATABASE="dapr-aks-e2e-tests"

[ -z "$DAPR_TEST_COSMOSDB_COLLECTION" ] && DAPR_TEST_COSMOSDB_COLLECTION="dapr-aks-e2e-state"

echo "Selected Kubernetes namespace: $DAPR_NAMESPACE"
echo "Selected Dapr Test Resource group: $DAPR_TEST_RESOURCE_GROUP"
echo "Selected Dapr Test CosmosDB account: $DAPR_TEST_COSMOSDB_ACCOUNT"
echo "Selected Dapr Test CosmosDB database: $DAPR_TEST_COSMOSDB_DATABASE"
echo "Selected Dapr Test CosmosDB collection: $DAPR_TEST_COSMOSDB_COLLECTION"

echo "Deleting existing collection ..."
az cosmosdb sql container delete -a $DAPR_TEST_COSMOSDB_ACCOUNT -g $DAPR_TEST_RESOURCE_GROUP -n $DAPR_TEST_COSMOSDB_COLLECTION -d $DAPR_TEST_COSMOSDB_DATABASE --yes
if [ $? -ne 0 ]; then
  echo "Failed to delete collection, skipping."
else
  echo "Deleted existing collection."
fi

echo "Creating collection ..."
az cosmosdb sql container create -g $DAPR_TEST_RESOURCE_GROUP -a $DAPR_TEST_COSMOSDB_ACCOUNT -d $DAPR_TEST_COSMOSDB_DATABASE -n $DAPR_TEST_COSMOSDB_COLLECTION --partition-key-path '/partitionKey'
if [ $? -ne 0 ]; then
  echo "Failed to create collection, aborting."
  exit 1
else
  echo "Created collection."
fi


echo "Deleting secret from Kubernetes cluster ..."
kubectl delete secret cosmosdb-secret --namespace=$DAPR_NAMESPACE
if [ $? -ne 0 ]; then
  echo "Failed to delete secret, skipping."
else
  echo "Deleted existing secret."
fi

PRIMARY_KEY=`az cosmosdb keys list --name $DAPR_TEST_COSMOSDB_ACCOUNT --resource-group $DAPR_TEST_RESOURCE_GROUP | jq .primaryMasterKey`
kubectl create secret generic cosmosdb-secret \
  --namespace=$DAPR_NAMESPACE \
  --from-literal=url=https://$DAPR_TEST_COSMOSDB_ACCOUNT.documents.azure.com:443/ \
  --from-literal=collection=$DAPR_TEST_COSMOSDB_COLLECTION \
  --from-literal=primaryMasterKey=$PRIMARY_KEY
if [ $? -ne 0 ]; then
  echo "Failed to create cosmosdb-secret."
  exit 1
fi

echo "Setup completed."