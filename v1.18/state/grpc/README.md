**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestStateGetGrpcPerformance | 999.98 | 15 | 30 | 4 | 44 | 52630.7 | 13824.2 |

### TestStateGetGrpcPerformance

<img src="TestStateGetGrpcPerformance_duration_breakdown.png" alt="TestStateGetGrpcPerformance_duration_breakdown.png" />
<img src="TestStateGetGrpcPerformance_duration_requested_vs_actual.png" alt="TestStateGetGrpcPerformance_duration_requested_vs_actual.png" />
<img src="TestStateGetGrpcPerformance_histogram_count.png" alt="TestStateGetGrpcPerformance_histogram_count.png" />
<img src="TestStateGetGrpcPerformance_histogram_percent.png" alt="TestStateGetGrpcPerformance_histogram_percent.png" />
<img src="TestStateGetGrpcPerformance_qps.png" alt="TestStateGetGrpcPerformance_qps.png" />
<img src="TestStateGetGrpcPerformance_resource_cpu.png" alt="TestStateGetGrpcPerformance_resource_cpu.png" />
<img src="TestStateGetGrpcPerformance_resource_memory.png" alt="TestStateGetGrpcPerformance_resource_memory.png" />
<img src="TestStateGetGrpcPerformance_summary.png" alt="TestStateGetGrpcPerformance_summary.png" />
<img src="TestStateGetGrpcPerformance_tail_latency.png" alt="TestStateGetGrpcPerformance_tail_latency.png" />
