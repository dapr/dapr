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

const httpReqDurationThreshold = __ENV.HTTP_REQ_DURATION_THRESHOLD

const possibleScenarios = {
    vu_10: {
        executor: 'shared-iterations',
        vus: 10,
        iterations: 100,
        maxDuration: '100s',     
    },
    vu_20: {
        executor: 'shared-iterations',
        vus: 20,
        iterations: 200,
        maxDuration: '100s',     
    },
    vu_30: {
        executor: 'shared-iterations',
        vus: 30,
        iterations: 300,
        maxDuration: '100s',     
    },
    vu_40: {
        executor: 'shared-iterations',
        vus: 40,
        iterations: 400,
        maxDuration: '200s',     
    },  
    vu_50: {
        executor: 'shared-iterations',
        vus: 50,
        iterations: 500,
        maxDuration: '200s',     
    }, 
    vu_60: {
        executor: 'shared-iterations',
        vus: 60,
        iterations: 600,
        maxDuration: '200s',     
    },       
    vu_70: {
        executor: 'shared-iterations',
        vus: 70,
        iterations: 700,
        maxDuration: '200s',     
    },     
    vu_80: {
        executor: 'shared-iterations',
        vus: 80,
        iterations: 800,
        maxDuration: '250s',     
    },     
    vu_90: {
        executor: 'shared-iterations',
        vus: 90,
        iterations: 900,
        maxDuration: '250s',     
    },     
    vu_100: {
        executor: 'shared-iterations',
        vus: 100,
        iterations: 1000,
        maxDuration: '1000s',     
    },         
    vu_120: {
        executor: 'shared-iterations',
        vus: 120,
        iterations: 1200,
        maxDuration: '1500s',     
    },          
    vu_130: {
        executor: 'shared-iterations',
        vus: 130,
        iterations: 1300,
        maxDuration: '1500s',     
    },          
    vu_140: {
        executor: 'shared-iterations',
        vus: 140,
        iterations: 1400,
        maxDuration: '1500s',     
    },            
    vu_150: {
        executor: 'shared-iterations',
        vus: 150,
        iterations: 1500,
        maxDuration: '1500s',     
    },          
    vu_170: {
        executor: 'shared-iterations',
        vus: 170,
        iterations: 1700,
        maxDuration: '1500s',     
    },           
    vu_200: {
        executor: 'shared-iterations',
        vus: 200,
        iterations: 2000,
        maxDuration: '1500s',     
    },                   
    vu_225: {
        executor: 'shared-iterations',
        vus: 225,
        iterations: 2250,
        maxDuration: '1500s',     
    },     
    vu_250: {
        executor: 'shared-iterations',
        vus: 250,
        iterations: 2500,
        maxDuration: '1500s',     
    },      
    vu_300: {
        executor: 'shared-iterations',
        vus: 300,
        iterations: 3000,
        maxDuration: '1500s',     
    },     
    vu_350: {
        executor: 'shared-iterations',
        vus: 350,
        iterations: 3500,
        maxDuration: '1500s',     
    },      
    vu_400: {
        executor: 'shared-iterations',
        vus: 400,
        iterations: 4000,
        maxDuration: '1500s',     
    },     
    vu_450: {
        executor: 'shared-iterations',
        vus: 450,
        iterations: 2250,
        maxDuration: '1500s',     
    },     
    vu_500: {
        executor: 'shared-iterations',
        vus: 500,
        iterations: 2500,
        maxDuration: '1500s',     
    },     
    vu_600: {
        executor: 'shared-iterations',
        vus: 600,
        iterations: 2000,
        maxDuration: '1500s',     
    },    
}

let enabledScenarios = {}
enabledScenarios[__ENV.SCENARIO] = possibleScenarios[__ENV.SCENARIO]

export const options = {
    discardResponseBodies: true,
    thresholds: {
        checks: [__ENV.RATE_CHECK],
        // Average of requests should be below HTTP_REQ_DURATION_THRESHOLD milliseconds
        // http_req_duration: ['avg<' + httpReqDurationThreshold],
    },
    scenarios: enabledScenarios,
}

const DAPR_ADDRESS = `http://127.0.0.1:${__ENV.DAPR_HTTP_PORT}`

function execute() {
    console.log("Executing the execute function with idInTest: " + `${exec.scenario.iterationInTest}`)
    let data = JSON.stringify({
        workflow_name : __ENV.WORKFLOW_NAME,
        workflow_input: __ENV.WORKFLOW_INPUT,
    })
    let params = {
        headers: {
            'Content-Type': 'application/json',
          },
        timeout: '250s'
    }
    const res =  http.post(`${__ENV.TARGET_URL}/${exec.scenario.iterationInTest}`,data,params)
    console.log("http response", JSON.stringify(res));
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
