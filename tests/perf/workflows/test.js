/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import http from 'k6/http'
import { check } from 'k6'

const possibleScenarios = {
    average_load: {
        executor: 'constant-vus',
        vus: 200000, // Key for Smoke test. Keep it at 2, 3, max 5 VUs
        duration: '2m',
    },
    stress_load: {
        executor: 'constant-vus',
        vus: 500, // Key for Smoke test. Keep it at 2, 3, max 5 VUs
        duration: '2m',
    },
    spike_load: {
        executor: 'ramping-vus',
        startVUs: 0,
        stages: [
            { duration: '10s', target: 100 },
            { duration: '5s', target: 0 },
        ],
        gracefulRampDown: '10s',
    },
    average_stress_spike_load: {
        executor: 'ramping-vus',
        stages: [
            // average load: 200 VUs run for 2 mins
          { duration: '10s', target: 200 },
          { duration: '2m', target: 200 },
          // stress load: 500 VUs run for 2 mins
          { duration: '10s', target: 500 },
          { duration: '2m', target: 500 },
          { duration: '20s', target: 0 },
          // Spike load: 200 VUs in 10s
          { duration: '10s', target: 200 },
          { duration: '5s', target: 0 },
        ],
        gracefulRampDown: '10s',
    },
    ramp_up: {
        executor: 'ramping-vus',
        startVUs: 500,
        stages: [
          { duration: '2m', target: 500 },
          { duration: '10s', target: 600 },
          { duration: '2m', target: 600 },
          { duration: '10s', target: 700 },
          { duration: '2m', target: 700 },
          { duration: '10s', target: 800 },
          { duration: '2m', target: 800 },
          { duration: '10s', target: 900 },
          { duration: '2m', target: 900 },
          { duration: '10s', target: 1000 },
          { duration: '2m', target: 1000 },
        ],
      },
}

let enabledScenarios = {}
enabledScenarios[__ENV.SCENARIO] = possibleScenarios[__ENV.SCENARIO]

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: [__ENV.RATE_CHECK],
    },
    scenarios: enabledScenarios,
}

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}`

export default function () {
    const res = http.get(`${__ENV.TARGET_URL}`)
    console.log("http response", JSON.stringify(res));
    check(result, {
        'response code was 2xx': (result) =>
            result.status >= 200 && result.status < 300,
    })
}

export function teardown(_) {
    const shutdownResult = http.post(`${DAPR_ADDRESS}/v1.0/shutdown`)
    check(shutdownResult, {
        'shutdown response status code is 2xx':
            shutdownResult.status >= 200 && shutdownResult.status < 300,
    })
}

export function handleSummary(data) {
    return {
        stdout: JSON.stringify(data),
    }
}