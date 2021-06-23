#!/usr/bin/env bash

# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

[ -z "$1" ] && echo "Namespace must be specified" && exit 0

installed_apps=$(helm list -q -n $1)
echo $installed_apps

for app in $installed_apps; do
    helm uninstall $app -n $1
done

kubectl delete crds components.dapr.io configurations.dapr.io subscriptions.dapr.io

echo "Trying to delete namespace..."
kubectl delete namespace $1 --timeout=10m

for pod in `kubectl get pods -n $1 -o name`; do
  kubectl delete --force -n $1 $pod
done

exit 0
