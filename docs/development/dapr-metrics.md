# Dapr metrics

Dapr metric name starts with `dapr_` prefix except for health metrics.

  * [Dapr Common metrics](#dapr-common-metrics)
  * [Dapr Operator metrics](#dapr-operator-metrics)
  * [Dapr Sidecar Injector metrics](#dapr-sidecar-injector-metrics)
  * [Dapr Placement metrics](#dapr-placement-metrics)
  * [Dapr Sentry metrics](#dapr-sentry-metrics)
  * [Dapr Runtime metrics](#dapr-runtime-metrics)
  * [Dapr Component metrics](#dapr-component-metrics)
  * [Dapr Scheduler metrics](#dapr-scheduler-metrics)

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
* dapr_placement_actor_heartbeat_timestamp: The actor's heartbeat timestamp (in seconds) was last reported to the placement service.
* dapr_placement_leader_status: Leadership status of the placement service (1 for leader, 0 for not leader).
* dapr_placement_raft_leader_status: Leadership status of the raft server (1 for leader, 0 for not leader).

## Dapr Sentry metrics

[monitoring metrics](../../pkg/sentry/monitoring/metrics.go)

* dapr_sentry_cert_sign_request_received_total: The number of CSRs received.
* dapr_sentry_cert_sign_success_total: The number of certificates issuances that have succeeded.
* dapr_sentry_cert_sign_failure_total: The number of errors occurred when signing the CSR.
* dapr_sentry_servercert_issue_failed_total: The number of server TLS certificate issuance failures.
* dapr_sentry_issuercert_changed_total: The number of issuer cert updates, when issuer cert or key is changed
* dapr_sentry_issuercert_expiry_timestamp: The unix timestamp, in seconds, when issuer/root cert will expire.

## Dapr Scheduler metrics

[monitoring metrics](../../pkg/scheduler/monitoring/metrics.go)

* dapr_scheduler_sidecars_connected: The total number of dapr sidecars connected to the scheduler service.
* dapr_scheduler_jobs_created_total: The total number of jobs scheduled.
* dapr_scheduler_jobs_triggered_total: The total number of successfully triggered jobs.
* dapr_scheduler_trigger_jobs_failed_total: The total number of failed jobs.
* dapr_scheduler_trigger_jobs_undelivered_total: The total number of undelivered jobs.
* dapr_scheduler_trigger_latency: The total time it takes to trigger a job from the scheduler service.

## Dapr Runtime metrics

### Error code metrics

[errorcode metrics](../../pkg/diagnostics/errorcode_monitoring.go)

* error_code_count: Number of times an error with a specific error code occurred.


### Service related metrics

[service metrics](../../pkg/diagnostics/service_monitoring.go)

#### Component

* dapr_runtime_component_loaded: The number of successfully loaded components
* dapr_runtime_component_init_total: The number of initialized components
* dapr_runtime_component_init_fail_total: The number of component initialization failures

#### Service Invocation

* dapr_runtime_service_invocation_req_sent_total: The number of remote service invocation requests sent
* dapr_runtime_service_invocation_req_recv_total: The number of remote service invocation requests received
* dapr_runtime_service_invocation_res_sent_total: The number of remote service invocation responses sent
* dapr_runtime_service_invocation_res_recv_total: The number of remote service invocation responses received
* dapr_runtime_service_invocation_res_recv_latency_ms: The remote service invocation round trip latency

#### Security

* dapr_runtime_mtls_init_total: The number of successful mTLS authenticator initialization.
* dapr_runtime_mtls_init_fail_total: The number of mTLS authenticator init failures
* dapr_runtime_mtls_workload_cert_rotated_total: The number of the successful workload certificate rotations
* dapr_runtime_mtls_workload_cert_rotated_fail_total: The number of the failed workload certificate rotations

#### Actors

* dapr_runtime_actor_status_report_total: The number of the successful status reports to placement service.
* dapr_runtime_actor_status_report_fail_total: The number of the failed status reports to placement service
* dapr_runtime_actor_table_operation_recv_total: The number of the received actor placement table operations.
* dapr_runtime_actor_rebalanced_total: The number of the actor rebalance requests.
* dapr_runtime_actor_deactivated_total: The number of the successful actor deactivation.
* dapr_runtime_actor_deactivated_failed_total: The number of the failed actor deactivation.
* dapr_runtime_actor_pending_actor_calls: The number of pending actor calls waiting to acquire the per-actor lock.
* dapr_runtime_actor_timers: The number of actor timers requests.
* dapr_runtime_actor_reminders: The number of actor reminders requests.
* dapr_runtime_actor_reminders_fired_total: The number of actor reminders fired requests.
* dapr_runtime_actor_timers_fired_total: The number of actor timers fired requests.

#### Resiliency

* dapr_resiliency_loaded: The number of resiliency policies loaded.
* dapr_resiliency_count: The number of times a resiliency policy has been executed.
* dapr_resiliency_activations_total: Number of times a resiliency policy has been activated in a building block after a failure or after a state change.
* dapr_resiliency_cb_state: A resiliency policy's current CircuitBreakerState state. 4 series are generated, one for each possible state, with the tag "status" being [unknown, closed, half-open, open]. The current state is 1, all other states are 0.

#### Workflow metrics

[workflow metrics](../../pkg/diagnostics/workflow_monitoring.go)

* dapr_runtime_workflow_operation_count: The number of successful/failed workflow operation requests.
* dapr_runtime_workflow_operation_latency: The latencies of responses for workflow operation requests.
* dapr_runtime_workflow_execution_count: The number of successful/failed/recoverable workflow executions.
* dapr_runtime_workflow_activity_operation_count: The number of successful/failed/recoverable activity requests.
* dapr_runtime_workflow_activity_operation_latency: The total time taken to run an activity request.
* dapr_runtime_workflow_activity_execution_count: The number of successful/failed/recoverable activity executions.
* dapr_runtime_workflow_activity_execution_latency: The total time taken to run an activity to completion.

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
> Note: Server metrics are prefixed by a forward slash character `/`

* dapr_http_server_request_count: Number of HTTP requests started in server
* dapr_http_server_request_bytes: HTTP request body size if set as ContentLength (uncompressed) in server
* dapr_http_server_response_count: Number of HTTP responses in server
* dapr_http_server_response_bytes: HTTP response body size (uncompressed) in server.
* dapr_http_server_latency: HTTP request end to end latency in server.

#### Client metrics

* dapr_http_client_sent_bytes: Total bytes sent in request body (not including headers)
* dapr_http_client_received_bytes: Total bytes received in response bodies (not including headers but including error responses with bodies)
* dapr_http_client_roundtrip_latency: End-to-end latency
* dapr_http_client_completed_count: Count of completed requests

## Dapr Component Metrics

### Pub/Sub metrics

* dapr_component_pubsub_ingress_latencies: The consuming app event processing latency
* dapr_component_pubsub_ingress_count: The number of incoming messages arriving from the pub/sub component
* dapr_component_pubsub_egress_count: The number of outgoing messages published to the pub/sub component
* dapr_component_pubsub_egress_latencies: The latency of the response from the pub/sub component

### Bindings metrics

* dapr_component_input_binding_count: The number of incoming events arriving from the input binding component
* dapr_component_input_binding_latencies: The triggered app event processing latency
* dapr_component_output_binding_count: The number of operations invoked on the output binding component
* dapr_component_output_binding_latencies: The latency of the response from the output binding component

### State metrics

* dapr_component_state_count: The number of operations performed on the state component
* dapr_component_state_latencies: The latency of the response from the state component

### Configuration metrics

* dapr_component_configuration_count: The number of operations performed on the configuration component
* dapr_component_configuration_latencies: The latency of the response from the configuration component

### Secret metrics

* dapr_component_secret_count: The number of operations performed on the secret component
* dapr_component_secret_latencies: The latency of the response from the secret component

