#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script kicks off the setup of Azure services for E2E tests.

# Usage: start_azure_setup.ps1

Start-Process nohup "pwsh -noprofile $PSScriptRoot\setup_cosmosdb.ps1"
Start-Process nohup "pwsh -noprofile $PSScriptRoot\setup_servicebus.ps1"
