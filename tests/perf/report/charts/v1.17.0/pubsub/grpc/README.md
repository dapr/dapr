## Highlights

**gRPC pubsub publish** (16 connections, 1,000 QPS):
- Median (p50): **0.60 ms** | p90: **0.94 ms** | p99: **1.87 ms** | p99.9: **2.89 ms**
- Dapr overhead vs. direct: **+0.53 ms** at p50, **+0.81 ms** at p90, **+1.72 ms** at p99
- 60,000 publish operations — **100% success rate** (all SERVING), 0 pod restarts
- Sidecar: 105 mCPU, 50 MB memory at sustained 1,000 QPS

With 60,000 publish operations sustaining 1,000 QPS, the Dapr sidecar received each message from the publisher and forwarded it to the message broker in a median of 0.60 ms — well under 1 ms. Nine out of ten requests complete in under 1 ms as well (p90 = 0.94 ms). The sidecar overhead of 0.53 ms at median accounts for message routing, topic resolution, and delivery confirmation. Across the entire run, every single publish succeeded with zero pod restarts.

---

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
