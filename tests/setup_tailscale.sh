#!/bin/bash

set -e

DAPR_TEST_NAMESPACE=${DAPR_TEST_NAMESPACE:-dapr-tests}
TAILSCALE_NAMESPACE=${TAILSCALE_NAMESPACE:-dapr-tests}

# Pod and service cidr used by tailscale subnet router
POD_CIDR=$(kubectl cluster-info dump | grep -m 1 cluster-cidr |grep -E -o "([0-9]{1,3}[\.]){3}[0-9]{1,3}/[0-9]{1,3}")
SERVICE_CIDR=$(echo '{"apiVersion":"v1","kind":"Service","metadata":{"name":"tst"},"spec":{"clusterIP":"1.1.1.1","ports":[{"port":443}]}}' | kubectl apply -f - 2>&1 | sed 's/.*valid IPs is //')

# Setup tailscale manifests
kubectl apply -f ./tests/config/tailscale_role.yaml --namespace $TAILSCALE_NAMESPACE
kubectl apply -f ./tests/config/tailscale_rolebinding.yaml --namespace $TAILSCALE_NAMESPACE
kubectl apply -f ./tests/config/tailscale_sa.yaml --namespace $TAILSCALE_NAMESPACE
sed -e "s;{{TS_AUTH_KEY}};$TAILSCALE_AUTH_KEY;g" ./tests/config/tailscale_key.yaml | kubectl apply --namespace $TAILSCALE_NAMESPACE -f -

# Set service CIDR and pod CIDR for the tailscale subrouter

sed -e "s;{{TS_ROUTES}};$SERVICE_CIDR,$POD_CIDR;g" ./tests/config/tailscale_subnet_router.yaml | kubectl apply --namespace $TAILSCALE_NAMESPACE -f -

# Wait for tailscale pod to be ready
for i in 1 2 3 4 5; do
    echo "waiting for the tailscale pod" && [[ $(kubectl get pods -l app=tailscale-subnet-router -n $TAILSCALE_NAMESPACE -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') == "True" ]] && break || sleep 10
done

if [[ $(kubectl get pods -l app=tailscale-subnet-router -n $TAILSCALE_NAMESPACE -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]; then
    echo "tailscale pod couldn't be ready"
    exit 1
fi

echo "tailscale pod is now ready"
sleep 5
kubectl logs -l app=tailscale-subnet-router -n $TAILSCALE_NAMESPACE
