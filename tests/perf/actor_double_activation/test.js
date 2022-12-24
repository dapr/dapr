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
import { Counter } from 'k6/metrics';

const DoubleActivations = new Counter('double_activations');

const singleId = "SINGLE_ID"
export const options = {
  discardResponseBodies: true,
  thresholds: {
    double_activations: ["count==0"],
  },
  scenarios: {
    doubleActivation: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "5s", target: 1000 },
        { duration: "5s", target: 3000 },
        { duration: "5s", target: 5000 },
      ],
      gracefulRampDown: "0s",
    },
  },
};

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}/v1.0`;

function callActorMethod(id, method) {
  return http.post(
    `${DAPR_ADDRESS}/actors/fake-actor-type/${id}/method/${method}`,
    JSON.stringify({})
  );
}
export default function () {
  const result = callActorMethod(singleId, "Lock");
  if (result.status >= 400) {
    DoubleActivations.add(1);
  }
}

export function teardown(_) {
  const shutdownResult = http.post(`${DAPR_ADDRESS}/shutdown`);
  check(shutdownResult, {
    "shutdown response status code is 2xx":
      shutdownResult.status >= 200 && shutdownResult.status < 300,
  });
}

export function handleSummary(data) {
  return {
    'stdout': JSON.stringify(data),
  };
}
