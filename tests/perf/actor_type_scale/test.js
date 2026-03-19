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

import http from 'k6/http'
import { check } from 'k6'
import exec from 'k6/execution'
import { SharedArray } from 'k6/data'

const defaultMethod = 'default'
const actorsTypes = __ENV.ACTORS_TYPES

const actors = new SharedArray('actors types', function () {
    return actorsTypes.split(',')
})

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: ['rate==1'],
        http_req_duration: ['p(95)<95'], // 95% of requests should be below 95ms
    },
    scenarios: {
        idStress: {
            executor: 'ramping-vus',
            startVUs: 0,
            stages: [
                { duration: '2s', target: 100 },
                { duration: '2s', target: 300 },
                { duration: '4s', target: 500 },
            ],
            gracefulRampDown: '0s',
        },
    },
}

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}/v1.0`

function callActorMethod(actor, id, method) {
    return http.put(
        `${DAPR_ADDRESS}/actors/${actor}/${id}/method/${method}`,
        JSON.stringify({})
    )
}
export default function () {
    const result = callActorMethod(
        actors[exec.scenario.iterationInTest % actors.length],
        exec.scenario.iterationInTest,
        defaultMethod
    )
    check(result, {
        'response code was 2xx': (result) =>
            result.status >= 200 && result.status < 300,
    })
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
