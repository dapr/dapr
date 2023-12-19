/*
Copyright 2021 The Dapr Authors
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

using Microsoft.AspNetCore.Mvc;
using System.Threading.Tasks;
using Dapr.Client;
using System;
using Dapr.Workflow;

namespace DaprDemoActor
{
  [ApiController]
  [Route("/")]
  public class Controller : ControllerBase
  {


    [HttpGet("/{instanceID}")]
    public async Task<ActionResult<string>> GetWorkflow([FromServices] DaprWorkflowClient workflowClient, [FromRoute] string instanceID)
    {
      var getResponse = await workflowClient.GetWorkflowStateAsync(instanceID);
      return Enum.GetName(typeof(WorkflowRuntimeStatus), getResponse.RuntimeStatus);
    }

    [HttpPost("StartWorkflow/{workflowName}/{instanceID}")]
    public async Task<ActionResult<string>> StartWorkflow([FromServices] DaprWorkflowClient workflowClient, [FromRoute] string instanceID, string workflowName)
    {
      var inputItem = "paperclips";
      var startResponse = await workflowClient.ScheduleNewWorkflowAsync(
              name: workflowName,
              input: inputItem,
              instanceId: instanceID);
      return startResponse;
    }

    [HttpPost("PurgeWorkflow/{instanceID}")]
    public async Task<ActionResult<bool>> PurgeWorkflow([FromServices] DaprWorkflowClient workflowClient, [FromRoute] string instanceID)
    {
      await workflowClient.PurgeInstanceAsync(instanceID);
      return true;
    }

    [HttpPost("TerminateWorkflow/{instanceID}")]
    public async Task<ActionResult<bool>> TerminateWorkflow([FromServices] DaprWorkflowClient workflowClient, [FromRoute] string instanceID)
    {
      await workflowClient.TerminateWorkflowAsync(instanceID);
      return true;
    }

    [HttpPost("PauseWorkflow/{instanceID}")]
    public async Task<ActionResult<bool>> PauseWorkflow([FromServices] DaprWorkflowClient workflowClient, [FromRoute] string instanceID)
    {
      await workflowClient.SuspendWorkflowAsync(instanceID);
      return true;
    }

    [HttpPost("ResumeWorkflow/{instanceID}")]
    public async Task<ActionResult<bool>> ResumeWorkflow([FromServices] DaprWorkflowClient workflowClient, [FromRoute] string instanceID)
    {
      await workflowClient.ResumeWorkflowAsync(instanceID);
      return true;
    }

    [HttpPost("RaiseWorkflowEvent/{instanceID}/{eventName}/{eventInput}")]
    public async Task<ActionResult<bool>> RaiseWorkflowEvent([FromServices] DaprWorkflowClient workflowClient, [FromRoute] string instanceID, string eventName, string eventInput)
    {
      await workflowClient.RaiseEventAsync(instanceID, eventName, eventInput);
      return true;
    }
  }
}
