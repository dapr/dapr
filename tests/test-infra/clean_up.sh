#!/usr/bin/env bash

# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

[ -z "$1" ] && echo "Namespace must be specified" && exit 0

installed_apps=$(helm list -q)

for app in $installed_apps; do
    helm delete --purge $app
done

echo "Trying to delete namespace..."
kubectl delete namespace $1

exit 0