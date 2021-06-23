#!/usr/bin/env bash

# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# This script scans all AKS clusters in test cluster pools to find the available test cluster

# Usage: find_cluster.sh <cluster_list_file>

# Define max e2e test timeout, 1.5 hours
[ -z "$MAX_TEST_TIMEOUT" ] && MAX_TEST_TIMEOUT=5400

[ -z "$DAPR_TEST_RESOURCE_GROUP" ] && DAPR_TEST_RESOURCE_GROUP="dapre2e"

if [ -z "$DAPR_TEST_NAMESPACE" ]; then
    if [ ! -z "$DAPR_NAMESPACE" ]; then
        DAPR_TEST_NAMESPACE=$DAPR_NAMESPACE
    else
        DAPR_TEST_NAMESPACE="dapr-tests"
    fi
fi

echo "Selected Dapr Test Resource group: $DAPR_TEST_RESOURCE_GROUP"
echo "Selected Kubernetes Namespace: $DAPR_TEST_NAMESPACE"

# Find the available cluster
for clustername in `cat $1 | sed 's/\r//g' | sort -R | xargs`; do
    echo "Scanning $clustername ..."

    echo "Switching to $clustername context..."
    # Switch to cluster context
    az aks get-credentials -n $clustername -g $DAPR_TEST_RESOURCE_GROUP
    if [ $? -ne 0 ]; then
        echo "Failed to switch to $clustername context. Retry to get available test cluster after 5 seconds ..."
        sleep 5
        continue
    fi

    # To resolve the race condition when multiple tests are running,
    # this script tries to create the namespace. If it is failed, we can assume
    # that this cluster is being used by the other tests.
    echo "Trying to create ${DAPR_TEST_NAMESPACE} namespace..."
    kubectl create namespace ${DAPR_TEST_NAMESPACE}
    if [ $? -eq 0 ]; then
        echo "Created ${DAPR_TEST_NAMESPACE} successfully and use $clustername cluster"
        echo "TEST_CLUSTER=$clustername" >> $GITHUB_ENV
        echo "DAPR_TAG=$clustername" >> $GITHUB_ENV
        echo "DAPR_TEST_TAG=$clustername" >> $GITHUB_ENV
        echo "TARGET_OS=$GOOS" >> $GITHUB_ENV
        echo "TARGET_ARCH=$GOARCH" >> $GITHUB_ENV
        exit 0
    fi

    # Get the running time of dapr-tests namespace
    start_datetime=$(kubectl get namespace ${DAPR_TEST_NAMESPACE} -o=jsonpath='{.metadata.creationTimestamp}')
    if [ $? -ne 0 ]; then
        echo "Cannot get creation time of namespace with kubectl (Test cluster is being cleaned up)."
        echo "Retry to get available test cluster after 5 seconds."
        sleep 5
        continue
    fi

    # NOTE: 'date' must be GNU date. Please use gdate on mac os
    # after installing coreutils (brew install coreutils)
    start_sec=$(date -d "$start_datetime" +%s)
    now_sec=$(date +%s)
    running_time=$((now_sec-start_sec))

    echo "Namespace is being used for $running_time seconds"

    # If running time is greater than $MAX_TEST_TIMEOUT seconds, it might be because of test failure.
    # In this case, we can use this cluster.
    if [ $running_time -gt $MAX_TEST_TIMEOUT ]; then
        echo "The previous test running in this cluster might be cancelled or failed accidentally so use $clustername cluster for e2e test."
        current_dir=$(dirname "$0")
        $current_dir/clean_up.sh ${DAPR_TEST_NAMESPACE}
    fi

    echo "-------------------------------------------------------"

    sleep 1
done

echo "All test clusters are fully occupied."

exit 1
