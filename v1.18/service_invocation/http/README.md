**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestServiceInvocationHTTPPerformance | 999.97 | 43 | 14 | 17 | 48 | 16666.1 | 16482.6 |

### TestServiceInvocationHTTPPerformance

<img src="TestServiceInvocationHTTPPerformance_connection_stats.png" alt="TestServiceInvocationHTTPPerformance_connection_stats.png" />
<img src="TestServiceInvocationHTTPPerformance_data_volume.png" alt="TestServiceInvocationHTTPPerformance_data_volume.png" />
<img src="TestServiceInvocationHTTPPerformance_duration_breakdown.png" alt="TestServiceInvocationHTTPPerformance_duration_breakdown.png" />
<img src="TestServiceInvocationHTTPPerformance_duration_requested_vs_actual.png" alt="TestServiceInvocationHTTPPerformance_duration_requested_vs_actual.png" />
<img src="TestServiceInvocationHTTPPerformance_header_size.png" alt="TestServiceInvocationHTTPPerformance_header_size.png" />
<img src="TestServiceInvocationHTTPPerformance_histogram_count.png" alt="TestServiceInvocationHTTPPerformance_histogram_count.png" />
<img src="TestServiceInvocationHTTPPerformance_histogram_percent.png" alt="TestServiceInvocationHTTPPerformance_histogram_percent.png" />
<img src="TestServiceInvocationHTTPPerformance_payload_size.png" alt="TestServiceInvocationHTTPPerformance_payload_size.png" />
<img src="TestServiceInvocationHTTPPerformance_qps.png" alt="TestServiceInvocationHTTPPerformance_qps.png" />
<img src="TestServiceInvocationHTTPPerformance_resource_cpu.png" alt="TestServiceInvocationHTTPPerformance_resource_cpu.png" />
<img src="TestServiceInvocationHTTPPerformance_resource_memory.png" alt="TestServiceInvocationHTTPPerformance_resource_memory.png" />
<img src="TestServiceInvocationHTTPPerformance_summary.png" alt="TestServiceInvocationHTTPPerformance_summary.png" />
<img src="TestServiceInvocationHTTPPerformance_tail_latency.png" alt="TestServiceInvocationHTTPPerformance_tail_latency.png" />
<img src="TestServiceInvocationHTTPPerformance_throughput.png" alt="TestServiceInvocationHTTPPerformance_throughput.png" />
