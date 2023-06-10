# Running Performance Tests

Performance tests are designed to let you evaluate the latency, resource usage and processing times for Dapr in your environment for a given hardware. The following describes how to run performance tests in a local dev environment and run them through CI:

  - [Run Performance tests in local dev environment](#run-perf-tests-in-local-dev-environment)
  - [Run Performance tests through GitHub Actions](#run-perf-tests-through-github-actions)

## Run Performance tests in local dev environment

### Prerequisites

* Kubernetes cluster (Minikube and Kind are valid options too).
  - To setup a new Kind cluster and local registry, run `make setup-kind`.
* Set up [Dapr development environment](https://github.com/dapr/dapr/blob/master/docs/development/setup-dapr-development-env.md)
  - [Install the latest Helm v3](https://helm.sh/docs/intro/install/).
* Create your DockerHub ID
* Create dapr-tests namespace
    ```bash
    kubectl create namespace dapr-tests
    ```
* Set the environment variables
    - If using Kind, run `make describe-kind-env` and copy-and-paste the export commands displayed.

    ```bash
    export DAPR_REGISTRY=docker.io/your_dockerhub_id
    export DAPR_TAG=dev
    export DAPR_NAMESPACE=dapr-tests

    # Do not set DAPR_TEST_ENV if you do not use minikube
    export DAPR_TEST_ENV=minikube

    # Set the below environment variables if you want to use the different registry and tag for test apps
    # export DAPR_TEST_REGISTRY=docker.io/your_dockerhub_id
    # export DARP_TEST_TAG=dev
    # export DAPR_TEST_REGISTRY_SECRET=yourself_private_image_secret

    # Set the below environment variables to configure test specific settings for Fortio based tests.
    # DAPR_PERF_QPS sets the desired number of requests per second. Default is 1.
    # DAPR_PERF_CONNECTIONS sets the number of client connections used to send requests to Dapr. Default is 1.
    # DAPR_TEST_DURATION sets the duration of the test. Default is "1m".
    # DAPR_PAYLOAD_SIZE sets a payload size in bytes to test with. default is 0.
    # DAPR_SIDECAR_CPU_LIMIT sets the cpu resource limit on the Dapr sidecar. default is 4.0.
    # DAPR_SIDECAR_MEMORY_LIMIT sets the memory resource limit on the Dapr sidecar. default is 512Mi.
    # DAPR_SIDECAR_CPU_REQUEST sets the cpu resource request on the Dapr sidecar. default is 0.5.
    # DAPR_SIDECAR_MEMORY_REQUEST sets the memory resource request on the Dapr sidecar. default is 250Mi.
    export DAPR_PERF_QPS
    export DAPR_PERF_CONNECTIONS
    export DAPR_TEST_DURATION
    export DAPR_PAYLOAD_SIZE
    export DAPR_SIDECAR_CPU_LIMIT
    export DAPR_SIDECAR_MEMORY_LIMIT
    export DAPR_SIDECAR_CPU_REQUEST
    export DAPR_SIDECAR_MEMORY_REQUEST

    ```

### Deploy your dapr runtime change

Run the below commands to build and deploy dapr from your local disk

```bash
# Build Linux binaries
make build-linux

# Build Docker image with Linux binaries
make docker-build

# Push docker image to your dockerhub registry
make docker-push

# Deploy Dapr runtime to your cluster
make docker-deploy-k8s

# Install 3rd party software
make setup-3rd-party
```

### Register app configurations

```bash
make setup-app-configurations
```

### Optional: Disable tracing

```bash
export DAPR_DISABLE_TELEMETRY=true
```

### Optional: Apply this configuration to disable mTLS

```bash
make setup-disable-mtls
```

### Register the default component configurations for testing

```bash
make setup-test-components
```

### Build and push test apps to docker hub

Build docker images from apps and push the images to test docker hub.

```bash
# build perf apps docker image under apps/
make build-perf-app-all

# push perf apps docker image to docker hub
make push-perf-app-all
```

You can also build and push the test apps individually.

```bash
# build perf apps docker image under apps/
make build-perf-app-<app-name>

# push perf apps docker image to docker hub
make push-perf-app-<app-name>
```

If you are building test apps individually, you need to build and push the tester app also:
- tester (`build-perf-app-tester` and `push-perf-app-tester`) for Fortio based tests
- k6-custom (`build-perf-app-k6-custom` and `push-perf-app-k6-custom`) for k6 based tests

### (k6) Install the k6-operator

If you are running k6 based tests, install the k6-operator.

```bash
make setup-test-env-k6
```

### Run performance tests

```bash
# start perf tests
make test-perf-all
```

## Run perf tests through GitHub Actions
To keep the build infrastructure simple, Dapr uses dapr-test GitHub Actions Workflow to run e2e tests using one of AKS clusters. A separate workflow also runs E2E in KinD clusters.

Once a contributor creates a pull request, E2E tests on KinD clusters are automatically executed for faster feedback. In order to run the E2E tests on AKS, ask a maintainer or approver to add /ok-to-perf comment to the Pull Request.




# Setup Required For Visualising Performance Test Metrics

The setup in AKS requires three servers for:
 - Prometheus 
 - Pushgateway
 - Grafana
 
All the servers should be installed in the same namespace to ensure effective communication between them.
 
 * Create a namesapce
  
    ```bash
    kubectl create namespace <namespace-name>
    ```
 * Set this as the current namespace
    
    ```bash
    kubectl config set-context --current --namespace=<namespace-name>
    ```
 * Check if the namespace is set
  
    ```bash
    kubectl config view | grep namespace:
    ```

## Setup for Prometheus Server

* Prometheus can be installed on AKS using the following commands:
 
  ```bash
  helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 
  helm repo update
  helm install prometheus prometheus-community/prometheus
  ```
* While installing this, there may be an error saying that some clusterrole or clusterrolebindings already exist. In that case delete the clusterrole or clusterrolebinding

  ```bash
  kubectl delete clusterrole <clusterrole-name>
  kubectl delete clusterrolebinding <clusterrolebinding-name>
  ```
* Check if the setup was properly installed

  ```bash
  kubectl get deployments -n <namespace-name>
  kubectl get pods -n <namespace-name>
  ```
  All the pods should show a running status.
  
* Forward port 9090 from your local machine to the pod where the prometheus-server is running

  ```bash
  kubectl port-forward -n <namespace-name> <prometheus-server-pod> 9090
  ```
* The Prometheus-server can now be accessed on localhost:9090.


## Setup for Prometheus Pushgateway

* There is no need for an individual set-up for pushgateway server as the Prometheus-pushgateway pod which was created while setting up prometheus on AKS can be used. This method is favorable as the connection between Prometheus and Pushgateway is already established in this case, and there is no need for setting up the individual configurations. 

* Forward port 9091 from your local machine to the prometheus-pushgateway pod and access it on localhost:9091 

  ```bash
  kubectl port-forward -n <namespace-name> <prometheus-pushgateway-pod> 9091
  ```

## Setup for Grafana Server

* Create a grafana.yaml file with the following configurations:

    ```yaml
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: grafana
      namespace: <namespace-name>
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: grafana
      template:
        metadata:
          labels:
            app: grafana
        spec:
          containers:
            - name: grafana
              image: grafana/grafana:latest
              ports:
                - containerPort: 3000
              env:
                - name: GF_INSTALL_PLUGINS
                  value: "grafana-piechart-panel,grafana-simple-json-datasource"
    ---
    apiVersion: v1
    kind: Service
    metadata:
      name: grafana
      namespace: <namespace-name>
    spec:
      type: LoadBalancer
      ports:
        - port: 80
          targetPort: 3000
          protocol: TCP
      selector:
        app: grafana
    ```
* Check if the setup was properly installed
  
  ```bash
  kubectl get deployments -n <namespace-name>
  kubectl get pods -n <namespace-name>
  ```
  This should show a grafana pod in the list of pods along with a running status for all.
  
* Apply the configurations
  
  ```bash
  kubectl apply -f <path to grafana.yaml file>
  ```
* Forward port 3000 from your local machine to the pod where grafana is running.
  
  ```bash
  kubectl port-forward -n <namespace-name> <grafana-pod> 3000
  ```
  The grafana server can now be accessed on localhost:3000
  
* Login to grafana with the default username and password 'admin' for both.

* Now go to data sources and connect Prometheus as a data source.

* The http URL will be the ClusterIP of the prometheus-server pod running on AKS which can be obtained by the command:
  
  ```bash
  kubectl get svc -n <namespace-name>
  ```
  So, the http URL will be http:// ClusterIP of prometheus-server pod
  
* [Grafana Dashboard for Perf Test](../config/grafana-perf-test-dashboard.json)
  
  On running the perf-tests now, the metrics are collected from pushgateway by prometheus and is made available for visualization as a dashboard by importing the above template in Grafana. 
