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
    $found = $false
    while(!$found) {
      kubectl describe secret $SecretName --namespace=$DaprNamespace
      if(!$?) {
          Write-Error "Did not find secret: SecretName."
          Start-Sleep -s 10
      } else {
        $found = $true
      }
    }
  }
  end{}
}

Verify-Secret $env:DAPR_NAMESPACE cosmosdb-secret
Verify-Secret $env:DAPR_NAMESPACE servicebus-secret

Write-Host "Setup verified."