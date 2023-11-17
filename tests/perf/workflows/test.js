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
import exec from 'k6/execution'
import { check } from 'k6'
import { Rate } from 'k6/metrics';

const errorRate = new Rate('errorRate');

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: [
            { threshold: 'rate > 0.9'},
        ],
    },
    vus: 100, // Key for Smoke test. Keep it at 2, 3, max 5 VUs
    duration: '10m',
    gracefulRampDown: '3m',
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