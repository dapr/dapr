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

const possibleScenarios = {
    t_30_300: {
        executor: 'shared-iterations',
        vus: 30,
        iterations: 300,
        maxDuration: '200s',
    },
    t_60_300: {
        executor: 'shared-iterations',
        vus: 60,
        iterations: 300,
        maxDuration: '380s',
    },
    t_90_300: {
        executor: 'shared-iterations',
        vus: 90,
        iterations: 300,
        maxDuration: '380s',
    },
    t_350_1400: {
        executor: 'shared-iterations',
        vus: 350,
        iterations: 1400,
        maxDuration: '1000s',
    },
    t_110_440: {
        executor: 'shared-iterations',
        vus: 110,
        iterations: 440,
        maxDuration: '450s',
    },
    t_80_800: {
        executor: 'shared-iterations',
        vus: 80,
        iterations: 800,
        maxDuration: '420s',
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

function execute() {
    console.log(
        'Executing the execute function with idInTest: ' +
            `${exec.scenario.iterationInTest}`
    )
    let data = JSON.stringify({
        workflow_name: __ENV.WORKFLOW_NAME,
        workflow_input: __ENV.WORKFLOW_INPUT,
    })
    let params = {
        headers: {
            'Content-Type': 'application/json',
        },
    }
    const res = http.post(
        `${__ENV.TARGET_URL}/${exec.scenario.iterationInTest}`,
        data,
        params
    )
    console.log('http response', JSON.stringify(res))
    return res
}

export default function () {
    let result = execute()
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
