#!/usr/bin/env pwsh
#
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
SetEnv "DAPR_TEST_QUERY_STATE_STORE" "cosmosdb_query"
# Temporarily use redis until we fix E2E to run with Azure Service Bus.
SetEnv "DAPR_TEST_PUBSUB" "servicebus"

SetEnv "DAPR_TEST_RESOURCE_GROUP" "dapre2e"
SetEnv "DAPR_TEST_COSMOSDB_ACCOUNT" "dapr-e2e-tests"
SetEnv "DAPR_TEST_COSMOSDB_DATABASE" "dapr-aks-e2e-tests"
SetEnv "DAPR_TEST_COSMOSDB_COLLECTION" $Env:TEST_CLUSTER
SetEnv "DAPR_TEST_COSMOSDB_QUERY_COLLECTION" ($Env:TEST_CLUSTER + "-query")
SetEnv "DAPR_TEST_SERVICEBUS_NAMESPACE" $Env:TEST_CLUSTER
