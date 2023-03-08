/*
Copyright 2022 The Dapr Authors
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

import { PodDisruptor } from 'k6/x/disruptor'
import http from 'k6/http'
import { check } from 'k6'
import { Counter } from 'k6/metrics'

const DoubleActivations = new Counter('double_activations')

const singleId = 'SINGLE_ID'
export const options = {
    discardResponseBodies: true,
    thresholds: {
        'double_activations{scenario:base}': ['count==0'],
        'double_activations{scenario:faults}': ['count==0'],
    },
    scenarios: {
        base: {
            executor: 'ramping-vus',
            startVUs: 0,
            stages: [
                { duration: '5s', target: 1000 },
                { duration: '5s', target: 3000 },
                { duration: '5s', target: 5000 },
            ],
            gracefulRampDown: '0s',
        },
        disrupt: {
            executor: 'shared-iterations',
            iterations: 1,
            vus: 1,
            exec: 'disrupt',
            startTime: '15s',
        },
        faults: {
            executor: 'ramping-vus',
            startVUs: 0,
            stages: [
                { duration: '5s', target: 1000 },
                { duration: '5s', target: 3000 },
                { duration: '5s', target: 5000 },
            ],
            gracefulRampDown: '0s',
            startTime: '15s',
        },
    },
}

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}/v1.0`

function callActorMethod(id, method) {
    return http.post(
        `${DAPR_ADDRESS}/actors/fake-actor-type/${id}/method/${method}`,
        JSON.stringify({})
    )
}
export default function () {
    const result = callActorMethod(singleId, 'Lock')
    if (result.status >= 400) {
        DoubleActivations.add(1)
    }
}

export function teardown(_) {
    const shutdownResult = http.post(`${DAPR_ADDRESS}/shutdown`)
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

// add disruption

const selector = {
    namespace: __ENV.TEST_NAMESPACE,
    select: {
        labels: {
            testapp: __ENV.TEST_APP_NAME,
        },
    },
}

export function disrupt() {
    const podDisruptor = new PodDisruptor(selector)

    // delay traffic from one random replica of the deployment
    const fault = {
        averageDelay: 50,
        errorCode: 500,
        errorRate: 0.1,
    }
    podDisruptor.injectHTTPFaults(fault, 15)
}
