import http from "k6/http";
import { check } from "k6";
import exec from "k6/execution";

const defaultMethod = "default"
const actorType = __ENV.ACTOR_TYPE

export const options = {
  discardResponseBodies: true,
  tresholds: {
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

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}/v1.0`;

function callActorMethod(id, method) {
  return http.put(
    `${DAPR_ADDRESS}/actors/${actorType}/${id}/method/${method}`,
    JSON.stringify({})
  );
}
export default function () {
  const result = callActorMethod(exec.scenario.iterationInTest, defaultMethod);
  check(result, {
    "response code was 2xx": (result) => result.status >= 200 && result.status < 300,
  })
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
