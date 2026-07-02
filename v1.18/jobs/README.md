**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestJobSchedulingPerformance | 870.79 | 62 | 27 | 102 | 80 | 5309.7 | 8344.7 |

### TestJobSchedulingPerformance

<img src="TestJobSchedulingPerformance_connection_stats.png" alt="TestJobSchedulingPerformance_connection_stats.png" />
<img src="TestJobSchedulingPerformance_data_volume.png" alt="TestJobSchedulingPerformance_data_volume.png" />
<img src="TestJobSchedulingPerformance_duration_breakdown.png" alt="TestJobSchedulingPerformance_duration_breakdown.png" />
<img src="TestJobSchedulingPerformance_duration_requested_vs_actual.png" alt="TestJobSchedulingPerformance_duration_requested_vs_actual.png" />
<img src="TestJobSchedulingPerformance_header_size.png" alt="TestJobSchedulingPerformance_header_size.png" />
<img src="TestJobSchedulingPerformance_histogram_count.png" alt="TestJobSchedulingPerformance_histogram_count.png" />
<img src="TestJobSchedulingPerformance_histogram_percent.png" alt="TestJobSchedulingPerformance_histogram_percent.png" />
<img src="TestJobSchedulingPerformance_payload_size.png" alt="TestJobSchedulingPerformance_payload_size.png" />
<img src="TestJobSchedulingPerformance_qps.png" alt="TestJobSchedulingPerformance_qps.png" />
<img src="TestJobSchedulingPerformance_resource_cpu.png" alt="TestJobSchedulingPerformance_resource_cpu.png" />
<img src="TestJobSchedulingPerformance_resource_memory.png" alt="TestJobSchedulingPerformance_resource_memory.png" />
<img src="TestJobSchedulingPerformance_summary.png" alt="TestJobSchedulingPerformance_summary.png" />
<img src="TestJobSchedulingPerformance_tail_latency.png" alt="TestJobSchedulingPerformance_tail_latency.png" />
<img src="TestJobSchedulingPerformance_throughput.png" alt="TestJobSchedulingPerformance_throughput.png" />
