## Highlights

All HTTP bulk publish variants completed with **100% success rate** and zero pod restarts.

| Variant | Throughput | p50 | p95 |
|---|---|---|---|
| memory-broker, batch=10, bulk | **13,635/sec** | 2.76 ms | 8.70 ms |
| memory-broker, batch=10, normal | 3,672/sec | 0.94 ms | 3.70 ms |
| memory-broker, batch=100, bulk | 2,654/sec | 13.82 ms | 48.06 ms |
| memory-broker, batch=100, normal | 384/sec | 0.92 ms | 3.47 ms |

Batch=10 bulk publish achieves **3.7× the throughput** of single-message (normal) at batch=10 — 13,635 iterations/sec vs. 3,672 iterations/sec. This is because each bulk call carries 10 messages in a single HTTP request, dramatically reducing per-message API and broker overhead. At batch=100, throughput drops to 2,654/sec because each request is larger and takes longer to process (p95 jumps to 48 ms), but the system still handles everything reliably. Note that normal publish p50 stays low (0.92–0.94 ms) because each individual message is small — the tradeoff is raw throughput capacity, not individual request speed. Kafka end-to-end publish+subscribe: p50=**3.97 ms**, p95=**8.78 ms**, 100% delivery success.

---

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
