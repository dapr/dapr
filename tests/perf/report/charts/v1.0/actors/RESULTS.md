# Actors Activation Performance

This article provides service invocation API performance benchmarks and resource utilization for actors in Dapr on Kubernetes.

## System overview

For applications using actors in Dapr there are two aspects to be considered. First, is the routing of actor invocations handled by Dapr sidecar. Second, is the actors runtime that is implemented and handled on the application side and depends on the SDK. For now, the performance tests are using the Java SDK to provide an actors runtime in the application.

### Kubernetes components

- Sidecar (data plane)
- Placement (required for actors, control plane mapping actor types to hosts)
- Operator (control plane)
- Sidecar Injector (control plane)
- Sentry (optional, control plane)

## Performance summary for Dapr v1.0

The actors API in Dapr sidecar will identify which hosts are registered for a given actor type and route the request to the appropriate host for a given actor ID. The host runs an instance of the application and uses the Dapr SDK (.Net, Java, Python or PHP) to handle actors requests via HTTP.

This test uses invokes actors via Dapr’s HTTP API directly.

For more information see [actors overview](https://docs.dapr.io/developing-applications/building-blocks/actors/actors-overview/).

### Kubernetes performance test setup

The test was conducted on a 3 node Kubernetes cluster, using commodity hardware running 4 cores and 8GB of RAM, without any network acceleration. The setup included a load tester ([Fortio](https://github.com/fortio/fortio)) pod with a Dapr sidecar injected into it that called the service invocation API to reach a pod on a different node.

Test parameters:

- 500 requests per second
- 1 replica
- 1 minute duration
- Sidecar limited to 0.5 vCPU
- mTLS enabled
- Sidecar telemetry enabled (tracing with a sampling rate of 0.1)
- Payload of an empty JSON object: {}

### Results

- The actual throughput was ~500 qps.
- The tp90 latency was ~3ms.
- The tp99 latency was ~6.2ms.
- Dapr app consumed ~523m CPU and ~304.7Mb of Memory
- Dapr sidecar consumed 2m CPU and ~18.2Mb of Memory
- No app restarts
- No sidecar restarts

Related links

For more information see overview of [Dapr on Kubernetes](https://docs.dapr.io/operations/hosting/kubernetes/kubernetes-overview/)