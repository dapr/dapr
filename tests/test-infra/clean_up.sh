#!/usr/bin/env bash

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
