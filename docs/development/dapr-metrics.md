# Dapr metrics

Dapr metric name starts with `dapr_` prefix except for health metrics.

  * [Dapr Common metrics](#dapr-common-metrics)
  * [Dapr Operator metrics](#dapr-operator-metrics)
  * [Dapr Sidecar Injector metrics](#dapr-sidecar-injector-metrics)
  * [Dapr Placement metrics](#dapr-placement-metrics)
  * [Dapr Sentry metrics](#dapr-sentry-metrics)
  * [Dapr Runtime metrics](#dapr-runtime-metrics)

## Dapr Common metrics

### Health metrics

Dapr uses prometheus process and go collectors by default.

* process_* : [prometheus process collector](https://github.com/prometheus/client_golang/blob/master/prometheus/process_collector.go)
* go_* : [prometheus go collector](https://github.com/prometheus/client_golang/blob/master/prometheus/go_collector.go)

## Dapr Operator metrics

[monitoring metrics](../../pkg/operator/monitoring/metrics.go)

* dapr_operator_service_created_total: The total number of dapr services created.
* dapr_operator_service_deleted_total: The total number of dapr services deleted.
* dapr_operator_service_updated_total: The total number of dapr services updated.

## Dapr Sidecar-injector metrics

[monitoring metrics](../../pkg/injector/monitoring/metrics.go)

* dapr_injector_sidecar_injection/requests_total: The total number of sidecar injection requests.
* dapr_injector_sidecar_injection/succeeded_total: The total number of successful sidecar injections.
* dapr_injector_sidecar_injection/failed_total: The total number of failed sidecar injections.

## Dapr Placement metrics

[monitoring metrics](../../pkg/placement/monitoring/metrics.go)

* dapr_placement_runtimes_total: The total number of hosts reported to placement service.
* dapr_placement_actorruntimes_total: The total number of actor runtimes reported to placement service.

## Dapr Sentry metrics

[monitoring metrics](../../pkg/sentry/monitoring/metrics.go)

* dapr_sentry_cert_sign_request_received_total: The number of CSRs received.
* dapr_sentry_cert_sign_success_total: The number of certificates issuances that have succeeded.
* dapr_sentry_cert_sign_failure_total: The number of errors occurred when signing the CSR.
* dapr_sentry_servercert_issue_failed_total: The number of server TLS certificate issuance failures.
* dapr_sentry_issuercert_changed_total: The number of issuer cert updates, when issuer cert or key is changed
* dapr_sentry_issuercert_expiry_timestamp: The unix timestamp, in seconds, when issuer/root cert will expire.

## Dapr Runtime metrics

### Service related metrics

[service metrics](../../pkg/diagnostics/service_monitoring.go)

#### Component

* dapr_runtime_component_loaded: The number of successfully loaded components
* dapr_runtime_component_init_total: The number of initialized components
* dapr_runtime_component_init_fail_total: The number of component initialization failures

#### Security

* dapr_runtime_mtls_init_total: The number of successful mTLS authenticator initialization.
* dapr_runtime_mtls_init_fail_total: The number of mTLS authenticator init failures
* dapr_runtime_mtls_workload_cert_rotated_total: The number of the successful workload certificate rotations
* dapr_runtime_mtls_workload_cert_rotated_fail_total: The number of the failed workload certificate rotations

#### Actors

* dapr_runtime_actor_status_report_total: The number of the successful status reports to placement service.
* dapr_runtime_actor_status_report_fail_total: The number of the failed status reports to placement service
* dapr_runtime_actor_table_operation_recv_total: The number of the received actor placement table operations.
* dapr_runtime_actor_reblanaced_total: The number of the actor rebalance requests.
* dapr_runtime_actor_activated_total: The number of the actor activation.
* dapr_runtime_actor_activated_failed_total: The number of the actor activation failures.
* dapr_runtime_actor_deactivated_total: The number of the successful actor deactivation.
* dapr_runtime_actor_deactivated_failed_total: The number of the failed actor deactivation.

### gRPC monitoring metrics

Dapr leverages opencensus ocgrpc plugin to generate gRPC server and client metrics.

* [server metrics](https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ocgrpc/server_metrics.go)
* [client_metrics](https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ocgrpc/client_metrics.go)

#### gRPC Server metrics

* dapr_grpc_io_server_received_bytes_per_rpc_*: Distribution of received bytes per RPC, by method.
* dapr_grpc_io_server_sent_bytes_per_rpc_*: Distribution of total sent bytes per RPC, by method.
* dapr_grpc_io_server_server_latency_*: Distribution of server latency in milliseconds, by method.
* dapr_grpc_io_server_completed_rpcs: Count of RPCs by method and status.

#### gRPC Client metrics

* dapr_grpc_io_client_sent_bytes_per_rpc: Distribution of bytes sent per RPC, by method.
* dapr_grpc_io_client_received_bytes_per_rpc_*: Distribution of bytes received per RPC, by method.
* dapr_grpc_io_client_completed_rpcs_*: Count of RPCs by method and status.

### HTTP monitoring metrics

We support only server side metrics.

* [server metrics](../../pkg/diagnostics/http_monitoring.go)

#### Server metrics

* dapr_http_server_request_count: Number of HTTP requests started in server
* dapr_http_server_request_bytes: HTTP request body size if set as ContentLength (uncompressed) in server
* dapr_http_server_response_bytes: HTTP response body size (uncompressed) in server.
* dapr_http_server_latency: HTTP request end to end latency in server.

#### Client metrics

* dapr_http/client/sent_bytes: Total bytes sent in request body (not including headers)
* dapr_http/client/received_bytes: Total bytes received in response bodies (not including headers but including error responses with bodies)
* dapr_http/client/roundtrip_latency: End-to-end latency
* dapr_http/client/completed_count: Count of completed requests
