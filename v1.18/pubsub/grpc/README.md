**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestPubsubPublishGrpcPerformance | 999.98 | 86 | 33 | 106 | 51 | 5208.3 | 12113.4 |

### TestPubsubPublishGrpcPerformance

<img src="TestPubsubPublishGrpcPerformance_duration_breakdown.png" alt="TestPubsubPublishGrpcPerformance_duration_breakdown.png" />
<img src="TestPubsubPublishGrpcPerformance_duration_requested_vs_actual.png" alt="TestPubsubPublishGrpcPerformance_duration_requested_vs_actual.png" />
<img src="TestPubsubPublishGrpcPerformance_histogram_count.png" alt="TestPubsubPublishGrpcPerformance_histogram_count.png" />
<img src="TestPubsubPublishGrpcPerformance_histogram_percent.png" alt="TestPubsubPublishGrpcPerformance_histogram_percent.png" />
<img src="TestPubsubPublishGrpcPerformance_qps.png" alt="TestPubsubPublishGrpcPerformance_qps.png" />
<img src="TestPubsubPublishGrpcPerformance_resource_cpu.png" alt="TestPubsubPublishGrpcPerformance_resource_cpu.png" />
<img src="TestPubsubPublishGrpcPerformance_resource_memory.png" alt="TestPubsubPublishGrpcPerformance_resource_memory.png" />
<img src="TestPubsubPublishGrpcPerformance_summary.png" alt="TestPubsubPublishGrpcPerformance_summary.png" />
<img src="TestPubsubPublishGrpcPerformance_tail_latency.png" alt="TestPubsubPublishGrpcPerformance_tail_latency.png" />
