
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
from dapr.ext.workflow import WorkflowRuntime, DaprWorkflowContext, WorkflowActivityContext, DaprWorkflowClient, WorkflowStatus, when_all
from dapr.conf import Settings
from dapr.clients import DaprClient
from dapr.clients.exceptions import DaprInternalError

settings = Settings()

api = Flask(__name__)

workflowComponent = "dapr"
workflowOptions = dict()
workflowOptions["task_queue"] =  "testQueue"
nonExistentIDError = "no such instance exists"
appPort = 3000 
state_store_name = "statestore"
actor_statestore_name = "statestore-actorstore"

# sum_series_wf calculates sum of numbers {1..input} by dividing the workload among 5 activties running in series
def sum_series_wf(ctx:DaprWorkflowContext):
    yield ctx.call_activity(sum_activity)
    yield ctx.call_activity(sum_activity)
    sum = yield ctx.call_activity(sum_activity)
    assert 42 == sum

# sum_activity calculates sum of numbers in range [num1,num2] (input:'num1,num2')
def sum_activity(ctx:WorkflowActivityContext):
    return 42

# start_workflow_runtime starts the workflow runtime and registers the declared workflows and activities
@api.route('/start-workflow-runtime', methods=['GET'])
def start_workflow_runtime():
    global workflowRuntime, workflowClient
    host = settings.DAPR_RUNTIME_HOST
    port = settings.DAPR_GRPC_PORT
    workflowRuntime = WorkflowRuntime(host, port)
    workflowRuntime.register_workflow(sum_series_wf)
    workflowRuntime.register_activity(sum_activity)
    workflowRuntime.start()
    workflowClient = DaprWorkflowClient(host=host,port=port)
    return "Workflow Runtime Started"

# shutdown_workflow_runtime stops the workflow runtime
@api.route('/shutdown-workflow-runtime', methods=['GET'])
def shutdown_workflow_runtime():
    workflowRuntime.shutdown()
    return "Workflow Runtime Shutdown"

# run_workflow runs an instance of workflow and waits for it to complete
@api.route('/run-workflow', methods=['GET'])
def run_workflow():
    with DaprClient() as d:
        print("==========Start Workflow:==========")

        try:
            instance_id = workflowClient.schedule_new_workflow(workflow=sum_series_wf)
            print(f'Workflow started. Instance ID: {instance_id}')
        except DaprInternalError as e:
            print(f"error starting workflow: {e.message}")
        
        workflow_state = workflowClient.wait_for_workflow_completion(
                instance_id=instance_id, timeout_in_seconds=250)
        assert workflow_state.runtime_status == WorkflowStatus.COMPLETED
        
        print("==========Get Workflow:==========")
        try:
            get_resp = d.get_workflow(instance_id=instance_id, workflow_component=workflowComponent)
            print(f"workflow instance_id {get_resp.instance_id} runtime_status {get_resp.runtime_status}")
        except DaprInternalError as e:
            print(f"error getting workflow status: {e.message}")

        print("==========Terminate Workflow:==========")
        try:
            d.terminate_workflow(instance_id=instance_id, workflow_component=workflowComponent)
        except DaprInternalError as e:
            print(f"error terminating workflow: {e.message}")
        
        print("==========Purge Workflow:==========")
        try:
            d.purge_workflow(instance_id=instance_id, workflow_component=workflowComponent)
        except DaprInternalError as e:
            print(f"error purging workflow: {e.message}")
        
        return "Workflow Run completed"

if __name__ == '__main__':
    api.run(host="0.0.0.0",port=appPort)