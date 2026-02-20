## Highlights

All gRPC bulk publish variants ran at 200 QPS target with **100% success rate** and zero pod restarts.

| Variant | Bulk QPS | vs. Single | p50 latency | p50 reduction |
|---|---|---|---|---|
| In-memory + cloud event | 199.94 | baseline | 10.79 ms | −47.22 ms |
| In-memory + raw payload | 199.93 | baseline | 10.61 ms | −36.11 ms |
| Kafka + cloud event | 199.92 | +119 QPS (91→200) | 18.33 ms | −138.11 ms |
| Kafka + raw payload | 199.92 | +82 QPS (110→200) | 18.05 ms | −117.21 ms |

The "p50 reduction" column shows how much faster each individual message arrives with bulk publish compared to sending messages one at a time. For Kafka, single-message publish was bottlenecked by per-broker-write overhead — each message required its own network round-trip to Kafka and an acknowledgment back. The Bulk Publish API batches multiple messages into a single write, amortizing that overhead across the batch. This is why Kafka sees the largest gains: 138.11 ms at p50 for the cloud event variant, and a 2.2× throughput improvement (91 → 200 QPS). In-memory brokers show smaller but still meaningful gains (47.22 ms reduction at p50) because the per-call overhead of the Dapr API itself is spread across the batch even when there's no external broker round-trip.

---

### TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event

<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_breakdown.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_duration_requested_vs_actual.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_count.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_histogram_percent.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_qps.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_cpu.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_resource_memory.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_summary.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_with_with_cloud_event_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)

<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_breakdown.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_count.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_histogram_percent.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_qps.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_cpu.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_resource_memory.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_summary.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_In-memory_without_cloud_event_(raw_payload)_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event

<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_breakdown.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_duration_requested_vs_actual.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_count.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_histogram_percent.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_qps.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_cpu.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_resource_memory.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_summary.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_with_cloud_event_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)

<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_breakdown.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_breakdown.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_duration_requested_vs_actual.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_count.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_count.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_percent.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_histogram_percent.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_qps.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_qps.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_cpu.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_cpu.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_memory.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_resource_memory.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_summary.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_summary.png" />
<img src="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_tail_latency.png" alt="TestBulkPubsubPublishGrpcPerformance_Kafka_without_cloud_event_(raw_payload)_tail_latency.png" />

### TestBulkPubsubPublishGrpcPerformance_variants_duration

<img src="TestBulkPubsubPublishGrpcPerformance_variants_duration.png" alt="TestBulkPubsubPublishGrpcPerformance_variants_duration.png" />
