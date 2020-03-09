## Dapr Runtime metrics

Dapr metric name starts with `dapr_` prefix except for health metrics.

### Health metrics

Dapr uses prometheus process and go collectors by default.

* process_* : [prometheus process collector](https://github.com/prometheus/client_golang/blob/master/prometheus/process_collector.go)
* go_* : [prometheus go collector](https://github.com/prometheus/client_golang/blob/master/prometheus/go_collector.go)

### Service related metrics

[service metrics](./service_monitoring.go)

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

Dapr uses opencensus ocgrpc plugin to generate server and client metrics.

* [server metrics](https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ocgrpc/server_metrics.go)
* [client_metrics](https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ocgrpc/client_metrics.go)

#### gRPC Server metrics

* dapr_grpc.io_server_received_bytes_per_rpc: Distribution of received bytes per RPC, by method.
* dapr_grpc.io_server_sent_bytes_per_rpc: Distribution of total sent bytes per RPC, by method.
* dapr_grpc.io_server_server_latency: Distribution of server latency in milliseconds, by method.
* dapr_grpc.io_server_completed_rpcs: Count of RPCs by method and status.
* dapr_grpc.io_server_received_messages_per_rpc: Distribution of messages received count per RPC, by method.
* dapr_grpc.io_server_sent_messages_per_rpc: Distribution of messages sent count per RPC, by method.

#### gRPC Client metrics

* dapr_grpc.io_client_sent_bytes_per_rpc: Distribution of bytes sent per RPC, by method.
* dapr_grpc.io_client_received_bytes_per_rpc: Distribution of bytes received per RPC, by method.
* dapr_grpc.io_client_roundtrip_latency: Distribution of round-trip latency, by method.
* dapr_grpc.io_client_completed_rpcs: Count of RPCs by method and status.
* dapr_grpc.io_client_sent_messages_per_rpc: Distribution of sent messages count per RPC, by method.
* dapr_grpc.io_client_received_messages_per_rpc: Distribution of received messages count per RPC, by method.
* dapr_grpc.io_client_server_latency: Distribution of server latency as viewed by client, by method.

### HTTP monitoring metrics

We support only server side metrics.

* [server metrics](./http_monitoring.go)

#### Server metrics

* dapr_http_server_request_count: Number of HTTP requests started in server
* dapr_http_server_request_bytes: HTTP request body size if set as ContentLength (uncompressed) in server
* dapr_http_server_response_bytes: HTTP response body size (uncompressed) in server.
* dapr_http_server_latency: HTTP request end to end latency in server.
