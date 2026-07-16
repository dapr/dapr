## Highlights

**HTTP pubsub publish**:
- Median (p50): **0.35 ms** | p90: **0.42 ms** | p95: **0.49 ms** | max: **2.90 ms**
- 1,606 publish operations — **100% success rate**, 0 errors

HTTP publish latency is the lowest of any Dapr publish path tested. The 0.35 ms median means the typical publish — from the app calling Dapr's HTTP API to the message being handed off to the broker — completes in under half a millisecond. The extremely tight p90–p95 band (0.42 ms to 0.49 ms) shows almost no variance: even the 95th-percentile request barely differs from the median. The maximum recorded latency of 2.90 ms confirms there are no outlier spikes anywhere in the distribution.

---

### TestPubsubPublishHttpPerformance

<img src="TestPubsubPublishHttpPerformance_data_volume.png" alt="TestPubsubPublishHttpPerformance_data_volume.png" />
<img src="TestPubsubPublishHttpPerformance_duration_breakdown.png" alt="TestPubsubPublishHttpPerformance_duration_breakdown.png" />
<img src="TestPubsubPublishHttpPerformance_duration_low.png" alt="TestPubsubPublishHttpPerformance_duration_low.png" />
<img src="TestPubsubPublishHttpPerformance_summary.png" alt="TestPubsubPublishHttpPerformance_summary.png" />
<img src="TestPubsubPublishHttpPerformance_tail_latency.png" alt="TestPubsubPublishHttpPerformance_tail_latency.png" />
<img src="TestPubsubPublishHttpPerformance_throughput.png" alt="TestPubsubPublishHttpPerformance_throughput.png" />
