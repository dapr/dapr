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
import exec from 'k6/execution'
import ws from 'k6/ws'
import { Counter } from 'k6/metrics'

const targetUrl = __ENV.TARGET_URL
const pubsubName = __ENV.PUBSUB_NAME
const subscribeType = __ENV.SUBSCRIBE_TYPE
const publishType = __ENV.PUBLISH_TYPE || 'bulk'
const httpReqDurationThreshold = __ENV.HTTP_REQ_DURATION_THRESHOLD

const defaultTopic = __ENV.PERF_PUBSUB_HTTP_TOPIC_NAME
const defaultCount = 100
const hundredBytesMessage = 'a'.repeat(100)

const testTimeoutMs = 60 * 1000
const errCounter = new Counter('error_counter')

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: ['rate==1'],
        // 95% of requests should be below HTTP_REQ_DURATION_THRESHOLD milliseconds
        http_req_duration: ['p(95)<' + httpReqDurationThreshold],
        error_counter: ['count==0'],
        'error_counter{errType:timeout}': ['count==0'],
    },
    scenarios: {
        bulkPSubscribe: {
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

/**
 * Publishes messages to a topic using the bulk publish API.
 * @param {String} pubsub pubsub component name
 * @param {String} topic topic name
 * @param {String} message message to publish
 * @param {Number} count number of messages to publish
 * @returns
 */
function publishMessages(pubsub, topic, message, count) {
    const bulkPublishBody = []
    for (let i = 0; i < count; i++) {
        bulkPublishBody.push({
            entryId: exec.vu.idInTest + '-' + i, // unique id for the message
            event: message,
            contentType: 'text/plain',
        })
    }

    return http.post(
        `${DAPR_ADDRESS}/v1.0-alpha1/publish/bulk/${pubsub}/${topic}`,
        JSON.stringify(bulkPublishBody),
        { headers: { 'Content-Type': 'application/json' } }
    )
}

/**
 * Publish a message to a topic using the publish API.
 * @param {String} pubsub
 * @param {String} topic
 * @param {String} message
 * @returns
 */
function publishMessage(pubsub, topic, message) {
    return http.post(
        `${DAPR_ADDRESS}/v1.0/publish/${pubsub}/${topic}`,
        message,
        { headers: { 'Content-Type': 'text/plain' } }
    )
}

export default function () {
    const url = `ws://${targetUrl}/test`
    const params = { tags: { subscribeType: subscribeType } }

    let topic = defaultTopic
    if (subscribeType === 'bulk') {
        topic = `${defaultTopic}-bulk`
    }

    const res = ws.connect(url, params, (socket) => {
        socket.on('open', () => {
            // Publish messages to the topic
            if (publishType === 'bulk') {
                let publishResponse = publishMessages(
                    pubsubName,
                    topic,
                    hundredBytesMessage,
                    defaultCount
                )
                check(publishResponse, {
                    'bulk publish response status code is 2xx': (r) =>
                        r.status >= 200 && r.status < 300,
                })
            } else {
                for (let i = 0; i < defaultCount; i++) {
                    const publishResponse = publishMessage(
                        pubsubName,
                        topic,
                        hundredBytesMessage
                    )
                    check(publishResponse, {
                        'publish response status code is 2xx': (r) =>
                            r.status >= 200 && r.status < 300,
                    })
                }
            }
        })

        socket.on('message', (data) => {
            console.log('Received data: ' + data)
            check(data, {
                'completed with success': (d) => d === 'true',
            })
        })

        socket.on('close', () => {
            console.log('closed')
        })

        socket.on('error', (err) => {
            if (err.error() != 'websocket: close sent') {
                console.log('Error: ' + err.error())
                errCounter.add(1)
            }
        })

        socket.setTimeout(() => {
            console.log('timeout reached, closing socket and failing test')
            errCounter.add(1, { errType: 'timeout' })
            socket.close()
        }, testTimeoutMs)
    })

    check(res, { 'status is 101': (r) => r && r.status === 101 })
}

export function teardown(_) {
    const shutdownResult = http.post(`${DAPR_ADDRESS}/v1.0/shutdown`)
    check(shutdownResult, {
        'shutdown response status code is 2xx': (r) =>
            r.status >= 200 && r.status < 300,
    })
}

export function handleSummary(data) {
    return {
        stdout: JSON.stringify(data),
    }
}
