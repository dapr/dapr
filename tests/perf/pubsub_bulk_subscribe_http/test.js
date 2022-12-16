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
const defaultTopic = "test-topic"
const defaultCount = 100
const oneKiloByteMessage = "a".repeat(1024)

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: ['rate==1'],
        http_req_duration: ['p(95)<200'], // 95% of requests should be below 200ms
    },
    scenarios: {
        idStress: {
            executor: "ramping-vus",
            startVUs: 0,
            stages: [
                { duration: "2s", target: 100 },
                { duration: "2s", target: 300 },
                { duration: "4s", target: 500 },
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
            entryId: i,
            event: message,
            contentType: "text/plain",
        });
    }
    return http.post(
        `${DAPR_ADDRESS}/v1.0-alpha/publish/bulk/${pubsub}/${topic}`,
        JSON.stringify(bulkPublishBody),
    );
}

export default function () {
    params = { type: subscribeType, topic: defaultTopic, pubsubName: pubsubName, count: defaultCount }

    const res = ws.connect(targetUrl, params, (socket) => {
        socket.on("open", () => {
            const publishResponse = publishMessages(pubsubName, defaultTopic, oneKiloByteMessage, defaultCount);
            check(publishResponse, {
                "publish response status code is 2xx": (r) => r.status >= 200 && r.status < 300
            });
        });
        socket.on("complete", (data) => {
            console.log("Received data: " + data);
            check(data, {
                "all messages received": (d) => d.count === defaultCount,
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
