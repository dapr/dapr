**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestStateGetHTTPPerformance | 999.97 | 87 | 30 | 94 | 49 | 5524.7 | 13024.5 |

### TestStateGetHTTPPerformance

<img src="TestStateGetHTTPPerformance_connection_stats.png" alt="TestStateGetHTTPPerformance_connection_stats.png" />
<img src="TestStateGetHTTPPerformance_data_volume.png" alt="TestStateGetHTTPPerformance_data_volume.png" />
<img src="TestStateGetHTTPPerformance_duration_breakdown.png" alt="TestStateGetHTTPPerformance_duration_breakdown.png" />
<img src="TestStateGetHTTPPerformance_duration_requested_vs_actual.png" alt="TestStateGetHTTPPerformance_duration_requested_vs_actual.png" />
<img src="TestStateGetHTTPPerformance_header_size.png" alt="TestStateGetHTTPPerformance_header_size.png" />
<img src="TestStateGetHTTPPerformance_histogram_count.png" alt="TestStateGetHTTPPerformance_histogram_count.png" />
<img src="TestStateGetHTTPPerformance_histogram_percent.png" alt="TestStateGetHTTPPerformance_histogram_percent.png" />
<img src="TestStateGetHTTPPerformance_payload_size.png" alt="TestStateGetHTTPPerformance_payload_size.png" />
<img src="TestStateGetHTTPPerformance_qps.png" alt="TestStateGetHTTPPerformance_qps.png" />
<img src="TestStateGetHTTPPerformance_resource_cpu.png" alt="TestStateGetHTTPPerformance_resource_cpu.png" />
<img src="TestStateGetHTTPPerformance_resource_memory.png" alt="TestStateGetHTTPPerformance_resource_memory.png" />
<img src="TestStateGetHTTPPerformance_summary.png" alt="TestStateGetHTTPPerformance_summary.png" />
<img src="TestStateGetHTTPPerformance_tail_latency.png" alt="TestStateGetHTTPPerformance_tail_latency.png" />
<img src="TestStateGetHTTPPerformance_throughput.png" alt="TestStateGetHTTPPerformance_throughput.png" />
