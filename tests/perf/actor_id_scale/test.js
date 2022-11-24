import http from "k6/http";
import { check } from "k6";
import exec from "k6/execution";

const defaultMethod = "default"
const actorType = __ENV.ACTOR_TYPE

export const options = {
  discardResponseBodies: true,
  scenarios: {
    idStress: {
      executor: 'constant-arrival-rate',
      rate: 100,
      timeUnit: '1s', // 100 iterations per second, i.e. 100 RPS
      duration: '10s',
      preAllocatedVUs: 10, // how large the initial pool of VUs would be
      maxVUs: 20, // if the preAllocatedVUs are not enough, we can initialize more
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
