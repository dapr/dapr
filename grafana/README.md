# Monitoring Dashboard

This includes dashboard templates to monitor Dapr system services and sidecars. For more detailed information, please read [Dapr Observability](https://docs.dapr.io/concepts/observability-concept/).

## Grafana

You can set up [Prometheus](https://docs.dapr.io/operations/monitoring/prometheus/) and [Grafana](https://docs.dapr.io/operations/monitoring/grafana/) and import the templates to your Grafana dashboard to monitor Dapr.

1. [Dapr System Service Dashboard](./grafana-system-services-dashboard.json)
    - Shows Dapr system service status - dapr-operator, dapr-sidecar-injector, dapr-sentry, and dapr-placement

2. [Dapr Sidecar Dashboard](./grafana-sidecar-dashboard.json)
    - Shows Dapr sidecar status - sidecar health/resources, throughput/latency of HTTP and gRPC, Actor, mTLS, etc.

3. [Dapr Actor Dashboard](./grafana-actor-dashboard.json)
    - Shows Dapr sidecar status - actor invocation throughput/latency, timer/reminder triggers, and turn-based concurrency.

## Reference

* [Supported Dapr metrics](../docs/development/dapr-metrics.md)
* [Dapr Observability](https://docs.dapr.io/concepts/observability-concept)
* [Setup Prometheus](https://docs.dapr.io/operations/monitoring/prometheus/)
* [Setup Grafana](https://docs.dapr.io/operations/monitoring/grafana/)
