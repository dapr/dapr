# Monitoring Dashboard

This includes dashboard templates to monitor Dapr system services and sidecars. For more detail information, please read [Dapr Observability](https://github.com/dapr/docs/blob/master/concepts/observability/README.md).

## Grafana

You can set up [Prometheus and Grafana](https://github.com/dapr/docs/blob/master/howto/setup-monitoring-tools/setup-prometheus-grafana.md) and import the templates to your Grafana dashboard to monitor Dapr.

1. [Dapr System Service Dashboard](./system-services-dashboard.json)
    - [Shows Dapr system component status](https://github.com/dapr/docs/blob/master/reference/dashboard/img/system-service-dashboard.png) - dapr-operator, dapr-sidecar-injector, dapr-sentry, and dapr-placement

2. [Dapr Sidecar Dashboard](./sidecar-dashboard.json)
    - [Shows Dapr Sidecar status](https://github.com/dapr/docs/blob/master/reference/dashboard/img/sidecar-dashboard.png) - sidecar health/resources, throughput/latency of HTTP and gRPC, Actor, mTLS, etc.

3. [Dapr Actor Dashboard](./actor-dashboard.json)
    - [Shows Dapr Sidecar status](https://github.com/dapr/docs/blob/master/reference/dashboard/img/actor-dashboard.png) - actor invocation throughput/latency, timer/reminder triggers, and turn-based concurrnecy.
