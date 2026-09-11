# Dapr metrics

Dapr metric name starts with `dapr_` prefix except for health metrics. Names below are the names
as scraped by a metrics exporter. The names in code use `/` separators, which the exporter replaces with `_`.

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

[monitoring metrics](../../pkg/injector/service/metrics.go)

* dapr_injector_sidecar_injection_requests_total: The total number of sidecar injection requests.
* dapr_injector_sidecar_injection_succeeded_total: The total number of successful sidecar injections.
* dapr_injector_sidecar_injection_failed_total: The total number of failed sidecar injections. Tagged with `reason`.

## Dapr Placement metrics

[monitoring metrics](../../pkg/placement/monitoring/metrics.go)

* dapr_placement_runtimes_total: The total number of hosts reported to placement service.
* dapr_placement_actor_runtimes_total: The total number of actor runtimes reported to placement service.
* dapr_placement_leader_status: Leadership status of the placement service (1 for leader, 0 for not leader).
* dapr_placement_raft_leader_status: Leadership status of the raft server (1 for leader, 0 for not leader).
* dapr_placement_actor_heartbeat_timestamp: The actor's heartbeat timestamp (in seconds) last reported to the placement service. Not available from 1.17 onwards.

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
* dapr_scheduler_jobs_created_failed_total: The total number of jobs that failed to be scheduled.
* dapr_scheduler_jobs_triggered_total: The total number of successfully triggered jobs.
* dapr_scheduler_jobs_failed_total: The total number of failed jobs.
* dapr_scheduler_jobs_undelivered_total: The total number of undelivered jobs.
* dapr_scheduler_jobs_deleted_total: The total number of deleted jobs.
* dapr_scheduler_jobs_bulk_deleted_total: The total number of jobs deleted by bulk delete.
* dapr_scheduler_trigger_latency: The total time it takes to trigger a job from the scheduler service.
* dapr_scheduler_sidecar_errors_total: The total number of errors returned by sidecars when triggering jobs.
* dapr_scheduler_concurrency_inflight: The number of job triggers currently in flight.
* dapr_scheduler_concurrency_pending: The number of job triggers waiting on a concurrency slot.
* dapr_scheduler_concurrency_throttled_total: The total number of job triggers throttled by concurrency limits.
* dapr_scheduler_placement_leader: Leadership status of the scheduler's placement service (1 for leader, 0 for not leader).
* dapr_scheduler_placement_streams_connected: The number of sidecar placement streams connected to the scheduler.
* dapr_scheduler_placement_disseminations_total: The total number of placement table disseminations.
* dapr_scheduler_placement_dissemination_latency: The time taken to disseminate a placement table.
* dapr_scheduler_placement_table_updates_total: The total number of placement table updates.
* dapr_scheduler_placement_incapable_sidecars: The number of connected sidecars that cannot use scheduler-hosted placement.

The Scheduler also exposes the collectors registered by its embedded etcd server on the same
endpoint. These register directly with the default Prometheus registry rather than through an
OpenCensus view, so they do not carry the `dapr` namespace:

* etcd_* : [etcd server metrics](https://etcd.io/docs/v3.5/metrics/), for example `etcd_server_has_leader`, `etcd_mvcc_db_total_size_in_bytes` and `etcd_server_quota_backend_bytes`
* grpc_* : [go-grpc-prometheus](https://github.com/grpc-ecosystem/go-grpc-prometheus) server metrics, registered by etcd's gRPC server
* os_* : etcd's file descriptor collector, `os_fd_used` and `os_fd_limit`

Set `--etcd-metrics=extensive` to include etcd's histogram metrics; the default is `basic`.

## Dapr Runtime metrics

### Error code metrics

[errorcode metrics](../../pkg/diagnostics/errorcode_monitoring.go)

* dapr_error_code_total: Number of times an error with a specific error code occurred. Only recorded when `spec.metrics.recordErrorCodes` is true on the associated Dapr Configuration resource.


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

* dapr_runtime_actor_status_report_total: The number of the successful status reports to placement service. Not available from 1.17 onwards.
* dapr_runtime_actor_status_report_fail_total: The number of the failed status reports to placement service. Not available from 1.17 onwards.
* dapr_runtime_actor_table_operation_recv_total: The number of the received actor placement table operations. Not available from 1.17 onwards.
* dapr_runtime_actor_rebalanced_total: The number of the actor rebalance requests.
* dapr_runtime_actor_deactivated_total: The number of the successful actor deactivation.
* dapr_runtime_actor_deactivated_failed_total: The number of the failed actor deactivation.
* dapr_runtime_actor_pending_actor_calls: The number of pending actor calls waiting to acquire the per-actor lock.
* dapr_runtime_actor_timers: The number of actor timers requests.
* dapr_runtime_actor_reminders: The number of actor reminders requests. Not available from 1.17 onwards.
* dapr_runtime_actor_reminders_fired_total: The number of actor reminders fired requests.
* dapr_runtime_actor_timers_fired_total: The number of actor timers fired requests.
* dapr_runtime_actor_timers_dropped_total: The number of actor timers dropped.

There is no metric for the number of currently active actors. Active actor counts per type are
available from the [metadata API](https://docs.dapr.io/reference/api/metadata_api/)
(`GET /v1.0/metadata`, under `actor_runtime.active_actors`).

#### Access control

Recorded only when access control policies are configured. The app and global policy metrics come
from the [access control allow list](https://docs.dapr.io/operations/configuration/invoke-allowlist/)
on the Configuration resource; the workflow metrics come from the WorkflowAccessPolicy resource.

* dapr_runtime_acl_app_policy_action_allowed_total: The number of requests allowed by an app access control policy.
* dapr_runtime_acl_app_policy_action_blocked_total: The number of requests blocked by an app access control policy.
* dapr_runtime_acl_global_policy_action_allowed_total: The number of requests allowed by the global access control policy.
* dapr_runtime_acl_global_policy_action_blocked_total: The number of requests blocked by the global access control policy.
* dapr_runtime_workflow_acl_action_allowed_total: The number of workflow requests allowed by access control.
* dapr_runtime_workflow_acl_action_denied_total: The number of workflow requests denied by access control.

#### Resiliency

* dapr_resiliency_loaded: The number of resiliency policies loaded.
* dapr_resiliency_count: The number of times a resiliency policy has been executed.
* dapr_resiliency_activations_total: Number of times a resiliency policy has been activated in a building block after a failure or after a state change.
* dapr_resiliency_cb_state: A resiliency policy's current CircuitBreakerState state. 4 series are generated, one for each possible state, with the tag "status" being [unknown, closed, half-open, open]. The current state is 1, all other states are 0.

#### Workflow metrics

[workflow metrics](../../pkg/diagnostics/workflow_monitoring.go)

* dapr_runtime_workflow_operation_count: The number of successful/failed workflow operation requests.
* dapr_runtime_workflow_operation_latency: The latencies of responses for workflow operation requests.
* dapr_runtime_workflow_execution_count: The number of successful/failed/terminated/recoverable workflow executions.
* dapr_runtime_workflow_execution_latency: The end-to-end time taken to run a workflow to completion.
* dapr_runtime_workflow_scheduling_latency: The delay between a workflow being requested and its execution starting.
* dapr_runtime_workflow_activity_operation_count: The number of successful/failed/recoverable activity requests.
* dapr_runtime_workflow_activity_operation_latency: The total time taken to run an activity request.
* dapr_runtime_workflow_activity_execution_count: The number of successful/failed/recoverable activity executions.
* dapr_runtime_workflow_activity_execution_latency: The total time taken to run an activity to completion.
* dapr_runtime_workflow_payload_size_ratio: Workflow dispatch payload size as a fraction of the configured gRPC `--max-body-size`; values >0.95 trip the graceful stall, values >1 exceed the limit. Not recorded when `--max-body-size` is non-positive.
* dapr_runtime_workflow_activity_payload_size_ratio: Activity dispatch payload size as a fraction of the configured gRPC `--max-body-size`; values >0.95 trip the graceful stall, values >1 exceed the limit. Not recorded when `--max-body-size` is non-positive.
* dapr_runtime_workflow_completion_route_count: The number of workflow completions by route.
* dapr_runtime_workflow_completions_fold_count: The number of folded workflow completions.
* dapr_runtime_workflow_completions_fold_wait_latency: The time spent waiting to fold workflow completions.
* dapr_runtime_workflow_local_wake_count: The number of local workflow wakes.
* dapr_runtime_workflow_local_wake_drive_latency: The time taken to drive a local workflow wake.
* dapr_runtime_workflow_local_activity_count: The number of local activity executions.
* dapr_runtime_workflow_local_activity_drive_latency: The time taken to drive a local activity.
* dapr_runtime_workflow_lock_wait: The time spent waiting on the workflow lock.
* dapr_runtime_workflow_attestation_generated_count: The number of workflow attestations generated.
* dapr_runtime_workflow_attestation_verified_count: The number of workflow attestations verified.
* dapr_runtime_workflow_attestation_verify_latency: The time taken to verify a workflow attestation.
* dapr_runtime_workflow_attestation_cert_cache_count: The number of workflow attestation certificate cache lookups.

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

* [http metrics](../../pkg/diagnostics/http_monitoring.go)

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

#### App health probe metrics

* dapr_http_healthprobes_completed_count: Count of completed app health probes
* dapr_http_healthprobes_roundtrip_latency: End-to-end latency of app health probes

## Dapr Component Metrics

### Pub/Sub metrics

* dapr_component_pubsub_ingress_latencies: The consuming app event processing latency
* dapr_component_pubsub_ingress_count: The number of incoming messages arriving from the pub/sub component
* dapr_component_pubsub_egress_count: The number of outgoing messages published to the pub/sub component
* dapr_component_pubsub_egress_latencies: The latency of the response from the pub/sub component
* dapr_component_pubsub_ingress_bulk_count: The number of incoming bulk requests arriving from the pub/sub component
* dapr_component_pubsub_ingress_bulk_event_count: The number of individual events inside incoming bulk requests
* dapr_component_pubsub_ingress_bulk_latencies: The consuming app bulk event processing latency
* dapr_component_pubsub_egress_bulk_count: The number of outgoing bulk requests published to the pub/sub component
* dapr_component_pubsub_egress_bulk_event_count: The number of individual events inside outgoing bulk requests
* dapr_component_pubsub_egress_bulk_latencies: The latency of the response to bulk publishes

Ingress metrics are tagged with `process_status` and `status`; egress metrics are tagged with
`success`. Both carry `app_id`, `component`, `namespace` and `topic`.

### Bindings metrics

* dapr_component_input_binding_count: The number of incoming events arriving from the input binding component
* dapr_component_input_binding_latencies: The triggered app event processing latency
* dapr_component_output_binding_count: The number of operations invoked on the output binding component
* dapr_component_output_binding_latencies: The latency of the response from the output binding component

### State metrics

* dapr_component_state_count: The number of operations performed on the state component
* dapr_component_state_latencies: The latency of the response from the state component

### Conversation metrics

* dapr_component_conversation_count: The number of operations performed on the conversation component
* dapr_component_conversation_latencies: The latency of the response from the conversation component

### Cryptography metrics

* dapr_component_crypto_count: The number of operations performed on the crypto component
* dapr_component_crypto_latencies: The latency of the response from the crypto component

### Configuration metrics

* dapr_component_configuration_count: The number of operations performed on the configuration component
* dapr_component_configuration_latencies: The latency of the response from the configuration component

### Secret metrics

* dapr_component_secret_count: The number of operations performed on the secret component
* dapr_component_secret_latencies: The latency of the response from the secret component

### Job metrics

* dapr_component_job_success_count: The number of successful job triggers
* dapr_component_job_failure_count: The number of failed job triggers
* dapr_component_job_latencies: The latency of the response from the app that processed the triggered job
