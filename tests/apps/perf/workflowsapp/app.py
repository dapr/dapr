
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

from flask import Flask
from datetime import datetime
from time import sleep
from dapr.ext.workflow import WorkflowRuntime, DaprWorkflowContext, WorkflowActivityContext, DaprWorkflowClient, WorkflowStatus
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

# chaining_wf calls a few activites in sequence
def chaining_wf(ctx:DaprWorkflowContext):
    print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{ctx.instance_id}] Invoked chaining_wf")
    yield ctx.call_activity(chaining_activity)
    yield ctx.call_activity(chaining_activity)
    sum = yield ctx.call_activity(chaining_activity)
    assert 42 == sum

# chaining_activity simply returns a number
def chaining_activity(ctx:WorkflowActivityContext):
    return 42

# start_workflow_runtime starts the workflow runtime and registers the declared workflows and activities
@api.route('/start-workflow-runtime', methods=['GET'])
def start_workflow_runtime():
    global workflowRuntime, workflowClient
    host = settings.DAPR_RUNTIME_HOST
    port = settings.DAPR_GRPC_PORT
    workflowRuntime = WorkflowRuntime(host, port)
    workflowRuntime.register_workflow(chaining_wf)
    workflowRuntime.register_activity(chaining_activity)
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
@api.route('/run-workflow/<run_id>', methods=['GET'])
def run_workflow(run_id):
    with DaprClient() as d:
        try:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] starting workflow")
            instance_id = workflowClient.schedule_new_workflow(workflow=chaining_wf)
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] started workflow with instance_id {instance_id}")
        except DaprInternalError as e:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] error starting workflow: {e.message}")
        
        sleep(0.5)

        workflow_state = workflowClient.wait_for_workflow_completion(
                instance_id=instance_id, timeout_in_seconds=250)
        assert workflow_state.runtime_status == WorkflowStatus.COMPLETED
        
        try:
            get_resp = d.get_workflow(instance_id=instance_id, workflow_component=workflowComponent)
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] workflow instance_id {get_resp.instance_id} runtime_status {get_resp.runtime_status}")
        except DaprInternalError as e:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] error getting workflow status: {e.message}")

        try:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] terminating workflow")
            d.terminate_workflow(instance_id=instance_id, workflow_component=workflowComponent)
        except DaprInternalError as e:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] error terminating workflow: {e.message}")
        
        try:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] purging workflow")
            d.purge_workflow(instance_id=instance_id, workflow_component=workflowComponent)
        except DaprInternalError as e:
            print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] error purging workflow: {e.message}")
        
        print(f"{datetime.now():%Y-%m-%d %H:%M:%S.%f} [{run_id}] workflow run complete")
        return "Workflow Run completed"

if __name__ == '__main__':
    api.run(host="0.0.0.0",port=appPort)