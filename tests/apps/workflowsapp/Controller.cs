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
    
    [HttpGet("{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<string>> GetWorkflow(DaprWorkflowClient daprClient,[FromRoute] string instanceID, string workflowComponent)
    {
      var getResponse = await daprClient.GetWorkflowStateAsync(instanceID);
      return getResponse.RuntimeStatus.ToString();
    }

    [HttpPost("StartWorkflow/{workflowComponent}/{workflowName}/{instanceID}")]
    public async Task<ActionResult<string>> StartWorkflow(DaprWorkflowClient daprClient,[FromRoute] string instanceID, string workflowName, string workflowComponent)
    {
      var inputItem = "paperclips";
      var startResponse = await daprClient.ScheduleNewWorkflowAsync(
              name:"PlaceOrder",
              instanceId: instanceID, 
              input: inputItem);

      var getResponse = await daprClient.GetWorkflowStateAsync(instanceID);

      return startResponse;
    }

    [HttpPost("PurgeWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> PurgeWorkflow(DaprWorkflowClient daprClient,[FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.PurgeInstanceAsync(instanceID);
      return true;
    }

    [HttpPost("TerminateWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> TerminateWorkflow(DaprWorkflowClient daprClient,[FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.TerminateWorkflowAsync(instanceID);
      return true;
    }

    [HttpPost("PauseWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> PauseWorkflow(DaprWorkflowClient daprClient,[FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.SuspendWorkflowAsync(instanceID);
      return true;
    }

    [HttpPost("ResumeWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> ResumeWorkflow(DaprWorkflowClient daprClient,[FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.ResumeWorkflowAsync(instanceID);
      return true;
    }

    [HttpPost("RaiseWorkflowEvent/{workflowComponent}/{instanceID}/{eventName}/{eventInput}")]
    public async Task<ActionResult<bool>> RaiseWorkflowEvent(DaprWorkflowClient daprClient,[FromRoute] string instanceID, string workflowComponent, string eventName, string eventInput)
    {
      await daprClient.RaiseEventAsync(instanceID, eventName, eventInput);
      return true;
    }
  }
}
