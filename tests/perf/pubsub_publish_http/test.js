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
import crypto from 'k6/crypto'
import { SharedArray } from 'k6/data'

const KB = 1024
const MAX_MS_ALLOWED = 1
// padd with leading 0 if <16
function i2hex(i) {
    return ('0' + i.toString(16)).slice(-2)
}

function randomStringOfSize(size) {
    const bytes = crypto.randomBytes(Math.round(size / 2)) // because of hex transformation
    const view = new Uint8Array(bytes)
    return view.reduce(function (memo, i) {
        return memo + i2hex(i)
    }, '')
}

const data = new SharedArray('scenarios', function () {
    const scenarioBase = {
        executor: 'constant-arrival-rate',
        rate: 1,
        preAllocatedVUs: 2,
        maxVUs: 50,
    }
    const delaysMs = [5, 50, 100, 1000]
    const messageSizeKb = [2, 31]
    const brokers = __ENV.BROKERS.split(',')
    const samples = [200]

    let scenarios = {}
    let thresholds = {
        checks: ['rate==1'],
    }

    let startTime = 0
    for (const messageSizeKbIdx in messageSizeKb) {
        const msgSize = messageSizeKb[messageSizeKbIdx]
        const msgStr = randomStringOfSize(msgSize * KB)
        for (const delayMsIdx in delaysMs) {
            const delay = delaysMs[delayMsIdx]
            for (const brokerIdx in brokers) {
                const broker = brokers[brokerIdx]
                for (const sampleIdx in samples) {
                    const sample = samples[sampleIdx]
                    const scenario = `${delay}ms_${msgSize}kb_${broker}_${sample}`
                    thresholds[`http_req_duration{scenario:${scenario}}`] = [
                        `avg<${MAX_MS_ALLOWED}`,
                    ]
                    const duration = delay * sample
                    scenarios[scenario] = Object.assign(
                        {
                            timeUnit: `${delay}ms`,
                            duration: `${duration}ms`,
                            env: {
                                MSG: msgStr,
                                BROKER: broker,
                                TOPIC: 'my-topic',
                            },
                            startTime: `${startTime}ms`,
                        },
                        scenarioBase
                    )
                    startTime += duration
                }
            }
        }
    }
    // more operations
    return [{ scenarios, thresholds }] // must be an array
})

const { scenarios, thresholds } = data[0]
export const options = {
    discardResponseBodies: true,
    thresholds,
    scenarios,
}

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}/v1.0`

function publishRawMsg(broker, topic, msg) {
    return http.post(
        `${DAPR_ADDRESS}/publish/${broker}/${topic}?metadata.rawPayload=true`,
        msg
    )
}
export default function () {
    const result = publishRawMsg(__ENV.BROKER, __ENV.TOPIC, __ENV.MSG)
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
