#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script verifies the setup of Azure components for E2E tests.

# Usage: start_azure_setup.ps1

function Verify-Secret(
    [string] $DaprNamespace,
    [string] $SecretName
) {
  begin{
    if([String]::IsNullOrWhiteSpace($DaprNamespace)){$DaprNamespace="default"}
  }
  process{
    kubectl describe secret $SecretName --namespace=$DaprNamespace
    if(!$?) {
        throw "Did not find secret: SecretName."
    }
  }
  end{}
}

Verify-Secret $env:DAPR_NAMESPACE cosmosdb-secret
Verify-Secret $env:DAPR_NAMESPACE servicebus-secret

Write-Host "Setup verified."