
# Copyright 2023 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
# 

from flask import Flask, request
from time import sleep
from datetime import datetime
from dapr.ext.workflow import WorkflowRuntime, DaprWorkflowContext, WorkflowActivityContext, DaprWorkflowClient, WorkflowStatus, when_all
from dapr.conf import Settings
from dapr.clients import DaprClient
from dapr.clients.exceptions import DaprInternalError
import json

settings = Settings()

api = Flask(__name__)

workflowComponent = "dapr"
workflowOptions = dict()
workflowOptions["task_queue"] =  "testQueue"
nonExistentIDError = "no such instance exists"
appPort = 3000 
state_store_name = "statestore"
actor_statestore_name = "statestore-actorstore"

# state_wf saves a key-value pair of `dataSize` size in the configured statestore, gets it back, validates and then deletes the key
def state_wf(ctx:DaprWorkflowContext,input):
    print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{ctx.instance_id}] Invoked state_wf")

    inputObj = json.loads(input)
    dataSize = int(inputObj["data_size"])
    key = inputObj["state_key"]
    data = "1" * dataSize
    print("Running state_wf with key=",key,"dataSize=",dataSize)

    save_input = {
        "key" : key,
        "value" : data,
    }
    yield ctx.call_activity(state_save_act, input = save_input)

    res = yield ctx.call_activity(state_get_act, input = key)
    assert res == data

    yield ctx.call_activity(state_delete_act,input=key)

# state_save_act saves the {key,value} pair in the configured statetsore
def state_save_act(ctx:WorkflowActivityContext,input):
    with DaprClient() as d:
        d.save_state(store_name=state_store_name,
                     key=input["key"],
                     value=input["value"])
        sleep(1)

# state_get_act gets the value stored in statestore against {key}
def state_get_act(ctx:WorkflowActivityContext, input):
    key = str(input)
    with DaprClient() as d:
        resp = d.get_state(store_name=state_store_name,
                           key=key)
        sleep(1)
        return resp.text()

# state_delete_act deletes the {key} from statestore
def state_delete_act(ctx:WorkflowActivityContext,input):
    key = str(input)
    with DaprClient() as d:
        d.delete_state(store_name=state_store_name,
                       key= key)
        sleep(1)

# sum_series_wf calculates sum of numbers {1..input} by dividing the workload among 5 activties running in series
def sum_series_wf(ctx:DaprWorkflowContext, input):
    print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{ctx.instance_id}] Invoked sum_series_wf")

    num = int(input)
    limit = int(num/5)
    sum1 = yield ctx.call_activity(sum_activity,input=f"1,{limit}")
    sum2 = yield ctx.call_activity(sum_activity,input=f"{limit+1},{2*limit}")
    sum3 = yield ctx.call_activity(sum_activity,input=f"{2*limit+1},{3*limit}")
    sum4 = yield ctx.call_activity(sum_activity,input=f"{3*limit+1},{4*limit}")
    sum5 = yield ctx.call_activity(sum_activity,input=f"{4*limit+1},{num}")

    total_sum = sum1 + sum2 + sum3 + sum4 + sum5
    expected_sum = (num*(num+1))/2
    assert expected_sum == total_sum

# sum_parallel_wf calculates sum of numbers {1..input} by dividing the workload among 5 activties running in parallel
def sum_parallel_wf(ctx:DaprWorkflowContext, input):
    print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{ctx.instance_id}] Invoked sum_parallel_wf")

    num = int(input)
    limit = int(num/5)
    inputs = [f"1,{limit}",f"{limit+1},{2*limit}",f"{2*limit+1},{3*limit}",f"{3*limit+1},{4*limit}",f"{4*limit+1},{num}"]
    parallel_tasks = [ctx.call_activity(sum_activity,input=input) for input in inputs]
    outputs = yield when_all(parallel_tasks)

    total_sum = 0
    for x in outputs:
        total_sum += int(x)
    expected_sum=(num*(num+1))/2
    assert expected_sum == total_sum

# sum_activity calculates sum of numbers in range [num1,num2] (input:'num1,num2')
def sum_activity(ctx:WorkflowActivityContext,input):
    limits = [int(x) for x in str(input).split(",")]
    num1 = limits[0]
    num2 = limits[1]
    sum = 0
    for i in range(num1,num2+1):
        sum += i
    return sum

# start_workflow_runtime starts the workflow runtime and registers the declared workflows and activities
@api.route('/start-workflow-runtime', methods=['GET'])
def start_workflow_runtime():
    global workflowRuntime, workflowClient
    host = settings.DAPR_RUNTIME_HOST
    port = settings.DAPR_GRPC_PORT
    workflowRuntime = WorkflowRuntime(host, port)
    workflowRuntime.register_workflow(sum_series_wf)
    workflowRuntime.register_workflow(sum_parallel_wf)
    workflowRuntime.register_workflow(state_wf)
    workflowRuntime.register_activity(sum_activity)
    workflowRuntime.register_activity(state_save_act)
    workflowRuntime.register_activity(state_get_act)
    workflowRuntime.register_activity(state_delete_act)
    workflowRuntime.start()
    workflowClient = DaprWorkflowClient(host=host,port=port)
    print("Workflow Runtime Started")
    return "Workflow Runtime Started"

# shutdown_workflow_runtime stops the workflow runtime
@api.route('/shutdown-workflow-runtime', methods=['GET'])
def shutdown_workflow_runtime():
    workflowRuntime.shutdown()
    print("Workflow Runtime Shutdown")
    return "Workflow Runtime Shutdown"

# run_workflow runs an instance of workflow and waits for it to complete
@api.route('/run-workflow/<run_id>', methods=['POST'])
def run_workflow(run_id):
    request_data = request.get_json()
    workflowName = request_data["workflow_name"]
    input = request_data["workflow_input"]

    # For state_wf, the input passed is `dataSize`. Adding `run_id` as the key for saving the data
    if workflowName == "state_wf":
        data_size = input
        input = json.dumps({
            "state_key": run_id,
            "data_size": data_size,
        })

    with DaprClient() as d:
        try:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] starting workflow")
            start_resp = d.start_workflow(workflow_component=workflowComponent,
                        workflow_name=workflowName, input=input, workflow_options=workflowOptions)
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] started workflow with instance_id {start_resp.instance_id}")
            instance_id = start_resp.instance_id
        except DaprInternalError as e:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] error starting workflow: {e.message}")

        sleep(0.5)

        workflow_state = workflowClient.wait_for_workflow_completion(instance_id=instance_id)
        assert workflow_state.runtime_status == WorkflowStatus.COMPLETED

        try:
            get_resp = d.get_workflow(instance_id=instance_id, workflow_component=workflowComponent)
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] workflow instance_id {get_resp.instance_id} runtime_status {get_resp.runtime_status}")
        except DaprInternalError as e:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] error getting workflow status: {e.message}")

        print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] workflow run complete")
        return "Workflow Run completed"

if __name__ == '__main__':
    api.run(host="0.0.0.0",port=appPort)
