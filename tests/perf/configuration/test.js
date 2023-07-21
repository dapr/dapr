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

const httpReqDurationThreshold = __ENV.HTTP_REQ_DURATION_THRESHOLD

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: ['rate==1'],
        // Average of requests should be below HTTP_REQ_DURATION_THRESHOLD milliseconds
        http_req_duration: ['avg<' + httpReqDurationThreshold],
    },
    scenarios: {
        configGet: {
            executor: 'ramping-vus',
            startVUs: 0,
            stages: [
                { duration: '2s', target: 100 },
                { duration: '3s', target: 300 },
                { duration: '4s', target: 500 },
            ],
            gracefulRampDown: '0s',
        },
    },
}

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}`

function execute() {
    return http.post(__ENV.TARGET_URL, __ENV.PAYLOAD, {
        headers: { 'Content-Type': 'application/json' },
    })
}

export default function () {
    let result = execute()
    console.log(result.json())
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
