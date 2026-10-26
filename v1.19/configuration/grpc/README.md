**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestConfigurationGetGRPCPerformance | 17044.93 | 601 | 102 | 134 | 84 | 23190.4 | 94048.3 |
| TestConfigurationSubscribeGRPCPerformance | 9641.97 | 220 | 75 | 88 | 80 | 31305.1 | 63727.5 |

### TestConfigurationGetGRPCPerformance

<img src="TestConfigurationGetGRPCPerformance_data_volume.png" alt="TestConfigurationGetGRPCPerformance_data_volume.png" />
<img src="TestConfigurationGetGRPCPerformance_duration_breakdown.png" alt="TestConfigurationGetGRPCPerformance_duration_breakdown.png" />
<img src="TestConfigurationGetGRPCPerformance_duration_low.png" alt="TestConfigurationGetGRPCPerformance_duration_low.png" />
<img src="TestConfigurationGetGRPCPerformance_resource_cpu.png" alt="TestConfigurationGetGRPCPerformance_resource_cpu.png" />
<img src="TestConfigurationGetGRPCPerformance_resource_memory.png" alt="TestConfigurationGetGRPCPerformance_resource_memory.png" />
<img src="TestConfigurationGetGRPCPerformance_summary.png" alt="TestConfigurationGetGRPCPerformance_summary.png" />
<img src="TestConfigurationGetGRPCPerformance_tail_latency.png" alt="TestConfigurationGetGRPCPerformance_tail_latency.png" />
<img src="TestConfigurationGetGRPCPerformance_throughput.png" alt="TestConfigurationGetGRPCPerformance_throughput.png" />

### TestConfigurationSubscribeGRPCPerformance

<img src="TestConfigurationSubscribeGRPCPerformance_data_volume.png" alt="TestConfigurationSubscribeGRPCPerformance_data_volume.png" />
<img src="TestConfigurationSubscribeGRPCPerformance_duration_breakdown.png" alt="TestConfigurationSubscribeGRPCPerformance_duration_breakdown.png" />
<img src="TestConfigurationSubscribeGRPCPerformance_duration_low.png" alt="TestConfigurationSubscribeGRPCPerformance_duration_low.png" />
<img src="TestConfigurationSubscribeGRPCPerformance_resource_cpu.png" alt="TestConfigurationSubscribeGRPCPerformance_resource_cpu.png" />
<img src="TestConfigurationSubscribeGRPCPerformance_resource_memory.png" alt="TestConfigurationSubscribeGRPCPerformance_resource_memory.png" />
<img src="TestConfigurationSubscribeGRPCPerformance_summary.png" alt="TestConfigurationSubscribeGRPCPerformance_summary.png" />
<img src="TestConfigurationSubscribeGRPCPerformance_tail_latency.png" alt="TestConfigurationSubscribeGRPCPerformance_tail_latency.png" />
<img src="TestConfigurationSubscribeGRPCPerformance_throughput.png" alt="TestConfigurationSubscribeGRPCPerformance_throughput.png" />
