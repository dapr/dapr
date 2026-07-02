**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestServiceInvocationGrpcPerformance | 999.96 | 91 | 14 | 51 | 49 | 7042.0 | 16075.5 |

### TestServiceInvocationGrpcPerformance

<img src="TestServiceInvocationGrpcPerformance_duration_breakdown.png" alt="TestServiceInvocationGrpcPerformance_duration_breakdown.png" />
<img src="TestServiceInvocationGrpcPerformance_duration_requested_vs_actual.png" alt="TestServiceInvocationGrpcPerformance_duration_requested_vs_actual.png" />
<img src="TestServiceInvocationGrpcPerformance_histogram_count.png" alt="TestServiceInvocationGrpcPerformance_histogram_count.png" />
<img src="TestServiceInvocationGrpcPerformance_histogram_percent.png" alt="TestServiceInvocationGrpcPerformance_histogram_percent.png" />
<img src="TestServiceInvocationGrpcPerformance_qps.png" alt="TestServiceInvocationGrpcPerformance_qps.png" />
<img src="TestServiceInvocationGrpcPerformance_resource_cpu.png" alt="TestServiceInvocationGrpcPerformance_resource_cpu.png" />
<img src="TestServiceInvocationGrpcPerformance_resource_memory.png" alt="TestServiceInvocationGrpcPerformance_resource_memory.png" />
<img src="TestServiceInvocationGrpcPerformance_summary.png" alt="TestServiceInvocationGrpcPerformance_summary.png" />
<img src="TestServiceInvocationGrpcPerformance_tail_latency.png" alt="TestServiceInvocationGrpcPerformance_tail_latency.png" />
