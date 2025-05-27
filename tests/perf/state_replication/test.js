import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate } from 'k6/metrics';

// Environment variables passed from the Go test runner
const DAPR_STATE_URL = __ENV.DAPR_STATE_URL || 'http://localhost:3500/v1.0/state/perf-redis-store'; // Default for local k6 run
const TEST_DURATION = __ENV.TEST_DURATION || '30s';
const VUS = __ENV.VUS || 10; // Number of virtual users
const KEY_COUNT = __ENV.KEY_COUNT || 1000; // Number of unique keys to write
const PAYLOAD_SIZE_BYTES = __ENV.PAYLOAD_SIZE_BYTES || 1024; // Size of the value in bytes

// Custom metrics
const writeLatency = new Trend('state_write_latency');
const writeSuccessRate = new Rate('state_write_success_rate');
const writeErrorRate = new Rate('state_write_error_rate');

export const options = {
  vus: VUS,
  duration: TEST_DURATION,
  thresholds: {
    'state_write_latency': ['p(95)<500'], // 95th percentile latency under 500ms
    'state_write_success_rate': ['rate>0.99'], // Over 99% success rate
  },
};

// Function to generate random payload
function generatePayload(size) {
  return 'a'.repeat(size);
}

const payload = generatePayload(PAYLOAD_SIZE_BYTES);

export default function () {
  const key = `testkey-${Math.floor(Math.random() * KEY_COUNT)}`;
  const state = [{
    key: key,
    value: {
      data: payload,
      timestamp: new Date().toISOString(),
    }
  }];

  const params = {
    headers: {
      'Content-Type': 'application/json',
    },
  };

  const res = http.post(DAPR_STATE_URL, JSON.stringify(state), params);

  // Record metrics
  writeLatency.add(res.timings.duration);
  const success = check(res, {
    'is status 201 (created) or 204 (updated)': (r) => r.status === 201 || r.status === 204,
  });

  if (success) {
    writeSuccessRate.add(1);
    writeErrorRate.add(0);
  } else {
    writeSuccessRate.add(0);
    writeErrorRate.add(1);
    console.error(`Error writing state: ${res.status} - ${res.body}`);
  }

  // Optional: Add a small sleep to simulate think time or control request rate
  // sleep(0.1); // 100ms
}
