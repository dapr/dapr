**Test environment:** Azure AKS, 3x Standard_D2s_v6 (2 vCPU, 8 GiB) Linux nodes

### Throughput per resource

Iterations/sec relative to the CPU and memory consumed by the app and its Dapr sidecar combined.
Resource figures are point-in-time samples taken at the end of each run: memory is steady-state and comparable across runs, but treat the per-core column as indicative only until resource usage is sampled continuously during the run.

| Test | Iterations/sec | App CPU (m) | App Mem (MB) | Sidecar CPU (m) | Sidecar Mem (MB) | Iter/s per core | Iter/s per GB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus | 67.55 | 24 | 19 | 2 | 37 | 2598.2 | 1230.4 |

### TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus

<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_data_volume.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_data_volume.png" />
<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" />
<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_low.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_duration_low.png" />
<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" />
<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" />
<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_summary.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_summary.png" />
<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" />
<img src="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_throughput.png" alt="TestPubsubBulkPublishBulkSubscribeHttpPerformance_kafka-messagebus_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk

<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_data_volume.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_breakdown.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_duration_low.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_summary.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_tail_latency.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_bulk_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal

<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_data_volume.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_breakdown.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_duration_low.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_summary.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_tail_latency.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b100_s1KB_normal_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk

<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_data_volume.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_breakdown.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_duration_low.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_summary.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_tail_latency.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_bulk_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal

<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_data_volume.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_data_volume.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_breakdown.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_breakdown.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_low.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_duration_low.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_summary.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_summary.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_tail_latency.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_tail_latency.png" />
<img src="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_throughput.png" alt="TestPubsubBulkPublishHttpPerformance_memory-broker_b10_s1KB_normal_throughput.png" />

### TestPubsubBulkPublishHttpPerformance_variants_duration

<img src="TestPubsubBulkPublishHttpPerformance_variants_duration.png" alt="TestPubsubBulkPublishHttpPerformance_variants_duration.png" />

### TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus

<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_data_volume.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_data_volume.png" />
<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_breakdown.png" />
<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_low.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_duration_low.png" />
<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_cpu.png" />
<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_resource_memory.png" />
<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_summary.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_summary.png" />
<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_tail_latency.png" />
<img src="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_throughput.png" alt="TestPubsubBulkPublishSubscribeHttpPerformance_kafka-messagebus_throughput.png" />
