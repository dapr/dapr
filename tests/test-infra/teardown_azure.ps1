#!/usr/bin/env pwsh
#
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script tears down Azure resources used for E2E tests.

# Usage: teardown_azure.ps1

pwsh -noprofile "$PSScriptRoot\teardown_servicebus.ps1"