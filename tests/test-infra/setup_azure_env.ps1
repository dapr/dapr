#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script sets up env variables to run E2E with Azure components.
# Why this scripts?
#  1. It removes this config from GH workflow file, enabling verification in PRs.
#  2. Allows GH workflow to be reused for multiple clouds.

# Usage: setup_azure_env.ps1

function SetEnv($Name, $Value)
{
    Write-Host "Writing $Name=$Value to env file"
    Write-Output "$Name=$Value" | Out-File -FilePath $Env:GITHUB_ENV -Encoding utf8 -Append
}


SetEnv "DAPR_TEST_STATE_STORE" "cosmosdb"
# Temporarily use redis until we fix E2E to run with Azure Service Bus.
SetEnv "DAPR_TEST_PUBSUB" "servicebus"

SetEnv "DAPR_TEST_RESOURCE_GROUP" "dapre2e"
SetEnv "DAPR_TEST_COSMOSDB_ACCOUNT" "dapr-e2e-tests"
SetEnv "DAPR_TEST_COSMOSDB_DATABASE" "dapr-aks-e2e-tests"
SetEnv "DAPR_TEST_COSMOSDB_COLLECTION" $Env:TEST_CLUSTER
SetEnv "DAPR_TEST_SERVICEBUS_NAMESPACE" $Env:TEST_CLUSTER