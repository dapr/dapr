#!/usr/bin/env bash

# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script scans all AKS clusters in test cluster pools to find the available test cluster

# The test cluster pool for e2e test
# TODO: Add more aks test clusters
testclusterpool=(
    "dapr-aks-e2e-01"
    "dapr-aks-e2e-02"
    "dapr-aks-e2e-03"
    "dapr-aks-e2e-04"
)

if [ -z "$DAPR_TEST_RESOURCE_GROUP" ]; then
    DAPR_TEST_RESOURCE_GROUP="dapre2e"
fi

if [ -z "$KUBE_TEST_NAMESPACE" ]; then
    KUBE_TEST_NAMESPACE="dapr-tests"
fi

echo "Selected Dapr Test Resource group: $DAPR_TEST_RESOURCE_GROUP"
echo "Selected Kubernetes Namespace: $KUBE_TEST_NAMESPACE"

# Find the available cluster
for clustername in ${testclusterpool[@]}; do
    echo "Scanning $clustername ..."

    echo "Switching to $clustername context..."
    # Switch to cluster context
    az aks get-credentials -n $clustername -g $DAPR_TEST_RESOURCE_GROUP
    if [ $? -ne 0 ]; then
        echo "Failed to switch to $clustername context. Retry to get available test cluster after 10 seconds ..."
        sleep 10
        continue
    fi

    echo "Checking if $KUBE_TEST_NAMESPACE namespace has any running pod ..."

    running_pods=$(kubectl get pods -o=jsonpath='{.items}' -n ${KUBE_TEST_NAMESPACE})
    if [ $? -ne 0 ]; then
        echo "Failed to run kubectl. Retry to get available test cluster after 10 seconds."
        sleep 5
        continue
    fi

    # If the namespace is empty, then items array is empty
    if [ "$running_pods" == "[]" ]; then
        echo "Found available test cluster: $clustername"
        exit 0
    fi

    # Get the running time for one of pods in dapr-tests namespace
    start_datetime=$(kubectl get pods -n ${KUBE_TEST_NAMESPACE} -o=jsonpath='{.items[0].status.startTime}')
    if [ $? -ne 0 ]; then
        echo "Cannot get startTime of the first pod with kubectl (Test cluster is being cleaned up)."
        echo "Retry to get available test cluster after 10 seconds."
        sleep 5
        continue
    fi

    # NOTE: 'date' is GNU version date. Please use gdate on mac os
    # after installing coreutils (brew install coreutils)
    start_sec=$(date -d "$start_datetime" +%s)
    now_sec=$(date +%s)
    running_time=$((now_sec-start_sec))

    echo "Found that the POD is running for $running_time seconds"

    # If running time is greater than 7200 seconds (2 hours), it might be because of test failure.
    # In this case, we can use this cluster after cleaning up the old pods
    if [ $running_time -gt 100 ]; then
        echo "The previous test running in this cluster might be cancelled or failed accidently so use $clustername cluster for e2e test."
        exit 0
    fi

    echo "-------------------------------------------------------"

    sleep 1
done

echo "All test clusters are fully occupied."

exit 1