# Monitoring Dashboard

This includes dashboard templates to monitor Dapr system services and sidecars. For more detail information, please read [Dapr Observability](https://docs.dapr.io/concepts/observability-concept/).

## Grafana

You can set up [Prometheus and Grafana](https://docs.dapr.io/operations/monitoring/prometheus/) and import the templates to your Grafana dashboard to monitor Dapr.

1. [Dapr System Service Dashboard](./grafana-system-services-dashboard.json)
    - Shows Dapr system component status - dapr-operator, dapr-sidecar-injector, dapr-sentry, and dapr-placement

2. [Dapr Sidecar Dashboard](./grafana-sidecar-dashboard.json)
    - Shows Dapr Sidecar status - sidecar health/resources, throughput/latency of HTTP and gRPC, Actor, mTLS, etc.

3. [Dapr Actor Dashboard](./grafana-actor-dashboard.json)
    - Shows Dapr Sidecar status - actor invocation throughput/latency, timer/reminder triggers, and turn-based concurrnecy.

## Reference

* [Supported Dapr metrics](../docs/development/dapr-metrics.md)
