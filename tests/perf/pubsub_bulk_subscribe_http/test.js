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

import http from "k6/http";
import { check } from "k6";
import ws from "k6/ws";

const targetUrl = __ENV.TARGET_URL
const pubsubName = __ENV.PUBSUB_NAME
const subscribeType = __ENV.SUBSCRIBE_TYPE
const defaultTopic = "perf-test"
const defaultCount = 100
const hundredBytesMessage = "a".repeat(100)

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: ['rate==1'],
        http_req_duration: ['p(95)<1000'], // 95% of requests should be below 1s
    },
    scenarios: {
        idStress: {
            executor: "ramping-vus",
            startVUs: 0,
            stages: [
                { duration: "2s", target: 10 },
                { duration: "3s", target: 30 },
                { duration: "4s", target: 50 },
            ],
            gracefulRampDown: "0s",
        },
    },
};

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}`;

/**
 * Publishes messages to a topic using the bulk publish API.
 * @param {String} pubsub pubsub component name
 * @param {String} topic topic name
 * @param {String} message message to publish
 * @param {Number} count number of messages to publish
 * @returns 
 */
function publishMessages(pubsub, topic, message, count) {
    const bulkPublishBody = [];
    for (let i = 0; i < count; i++) {
        bulkPublishBody.push({
            entryId: i.toString(),
            event: message,
            contentType: "text/plain",
        });
    }

    return http.post(
        `${DAPR_ADDRESS}/v1.0-alpha1/publish/bulk/${pubsub}/${topic}`,
        JSON.stringify(bulkPublishBody),
        { headers: { 'Content-Type': 'application/json' }, });
}

export default function () {
    const url = `ws://${targetUrl}/test`
    const params = { type: subscribeType, count: defaultCount }

    const res = ws.connect(url, params, (socket) => {
        socket.on("open", () => {
            const publishResponse = publishMessages(pubsubName, defaultTopic, hundredBytesMessage, defaultCount);
            check(publishResponse, {
                "publish response status code is 2xx": (r) => r.status >= 200 && r.status < 300
            });
        });
        socket.on("message", (data) => {
            console.log("Received data: " + data);
            check(data, {
                "completed with success": (d) => d === "true",
            })
        });
        socket.on("close", () => {
            console.log("closed");
        });
        socket.on("error", (err) => {
            console.log(err);
        });
    });

    check(res, { "status is 101": (r) => r && r.status === 101 });
}

export function teardown(_) {
    const shutdownResult = http.post(`${DAPR_ADDRESS}/v1.0/shutdown`);
    check(shutdownResult, {
        "shutdown response status code is 2xx": (r) => r.status >= 200 && r.status < 300,
    });
}

export function handleSummary(data) {
    return {
        'stdout': JSON.stringify(data),
    };
}
