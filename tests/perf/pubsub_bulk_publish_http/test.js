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

/**
 * Get the payload for a bulk publish request
 * @param {number} numMsgs number of messages to publish at once
 * @param {number} msgSize size of each message in KB
 */
function getBulkPublishPayload(numMsgs, msgSize) {
    const entries = []
    for (let i = 0; i < numMsgs; i++) {
        const entry = {
            entryId: `${i}`,
            event: randomStringOfSize(msgSize * KB),
            contentType: 'text/plain',
        }
        entries.push(entry)
    }
    return JSON.stringify(entries)
}

const data = new SharedArray('scenarios', function () {
    const thresholds = {
        checks: ['rate==1'],
        http_req_duration: [`avg<${MAX_MS_ALLOWED}`]
    };

    const brokerName = __ENV.BROKER_NAME;
    const topicName = __ENV.TOPIC_NAME;
    const numMessages = __ENV.NUM_MESSAGES;
    const bulkSize = parseInt(__ENV.BULK_SIZE);
    const messageSizeKb = parseInt(__ENV.MESSAGE_SIZE_KB);
    const durationMs = parseInt(__ENV.DURATION_MS);
    const numVus = parseInt(__ENV.NUM_VUS);

    const numIterations = Math.floor(numMessages / bulkSize);

    const scenario = `${brokerName}_n${numMessages}_b${bulkSize}_s${messageSizeKb}KB`;
    let scenarios = {};

    scenarios[scenario] = {
        executor: 'constant-vus',
        vus: numVus,
        duration: `${durationMs}ms`,
        env: {
            PAYLOAD: getBulkPublishPayload(bulkSize, messageSizeKb),
            BROKER: brokerName,
            TOPIC: topicName,
            NUM_ITER: numIterations,
        }
    };

    return [{ scenarios, thresholds }] // must be an array
})

const { scenarios, thresholds } = data[0]
export const options = {
    discardResponseBodies: true,
    thresholds,
    scenarios
}

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}`

function publishRawMsgs(broker, topic, payload, numIter) {
    statusCodes = []
    for (let i = 0; i < numIter; i++) {
        const result = http.post(
            `${DAPR_ADDRESS}/v1.0-alpha1/publish/bulk/${broker}/${topic}?metadata.rawPayload=true`,
            payload
        )
        statusCodes.push(result.status)
    }

    return statusCodes;
}

export default function () {
    const statusCodes = publishRawMsgs(__ENV.BROKER, __ENV.TOPIC, __ENV.PAYLOAD, __ENV.NUM_ITER)
    const failed = statusCodes.filter(code => code >= 300)

    if (failed.length > 0) {
        console.log(`Failed to publish ${failed.length} messages`)
        console.log(`Status codes: ${JSON.stringify(statusCodes)}`)
    }

    check(failed, {
        'all messages published successfully': (failed) => failed.length === 0,
    })
}

export function teardown(_) {
    const shutdownResult = http.post(`${DAPR_ADDRESS}/v1.0/shutdown`)
    if (shutdownResult.status >= 300) {
        console.log(`Shutdown failed: ${shutdownResult.status}`)
    }

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
