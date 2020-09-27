#!/usr/bin/env bash

# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

[ -z "$1" ] && echo "Namespace must be specified" && exit 0

installed_apps=$(helm list -q -n $1)
echo $installed_apps

for app in $installed_apps; do
    helm uninstall $app -n $1
done

echo "Trying to delete namespace..."
kubectl delete namespace $1 --timeout=10m

exit 0