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

namespace DaprDemoActor
{
  [ApiController]
  [Route("/")]
  public class Controller : ControllerBase
  {
    static string httpEndpoint = "http://127.0.0.1:" + Environment.GetEnvironmentVariable("DAPR_HTTP_PORT");
    static string grpcEndpoint = "http://127.0.0.1:" + Environment.GetEnvironmentVariable("DAPR_GRPC_PORT");
    public static DaprClient daprClient = new DaprClientBuilder().UseGrpcEndpoint(grpcEndpoint).UseHttpEndpoint(httpEndpoint).Build();

    [HttpGet("{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<string>> GetWorkflow([FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.WaitForSidecarAsync();
      var getResponse = await daprClient.GetWorkflowAsync(instanceID, workflowComponent);
      return getResponse.RuntimeStatus.ToString();
    }

    [HttpPost("StartWorkflow/{workflowComponent}/{workflowName}/{instanceID}")]
    public async Task<ActionResult<string>> StartWorkflow([FromRoute] string instanceID, string workflowName, string workflowComponent)
    {
      await daprClient.WaitForSidecarAsync();
      var inputItem = "paperclips";
      var workflowOptions = new Dictionary<string, string>();
      var startResponse = await daprClient.StartWorkflowAsync(
              instanceId: instanceID, 
              workflowComponent: workflowComponent,
              workflowName: "PlaceOrder",
              input: inputItem,
              workflowOptions: workflowOptions);

      var getResponse = await daprClient.GetWorkflowAsync(instanceID, workflowComponent);

      return getResponse.InstanceId;
    }

    [HttpPost("StartMonitorWorkflow/{workflowComponent}/{watchInstanceID}/{instanceID}")]
    public async Task<ActionResult<string>> StartMonitorWorkflow([FromRoute] string watchInstanceID, string instanceID,  string workflowComponent)
    {
      await daprClient.WaitForSidecarAsync();
      var inputItem = watchInstanceID;
      var workflowOptions = new Dictionary<string, string>();
      var startResponse = await daprClient.StartWorkflowAsync(
              instanceId: instanceID, 
              workflowComponent: workflowComponent,
              workflowName: "Monitor",
              input: inputItem,
              workflowOptions: workflowOptions);

      var getResponse = await daprClient.GetWorkflowAsync(instanceID, workflowComponent);

      return getResponse.InstanceId;
    }

    [HttpPost("PurgeWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> PurgeWorkflow([FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.PurgeWorkflowAsync(instanceID, workflowComponent);
      return true;
    }

    [HttpPost("TerminateWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> TerminateWorkflow([FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.TerminateWorkflowAsync(instanceID, workflowComponent);
      return true;
    }

    [HttpPost("PauseWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> PauseWorkflow([FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.PauseWorkflowAsync(instanceID, workflowComponent);
      return true;
    }

    [HttpPost("ResumeWorkflow/{workflowComponent}/{instanceID}")]
    public async Task<ActionResult<bool>> ResumeWorkflow([FromRoute] string instanceID, string workflowComponent)
    {
      await daprClient.ResumeWorkflowAsync(instanceID, workflowComponent);
      return true;
    }

    [HttpPost("RaiseWorkflowEvent/{workflowComponent}/{instanceID}/{eventName}/{eventInput}")]
    public async Task<ActionResult<bool>> RaiseWorkflowEvent([FromRoute] string instanceID, string workflowComponent, string eventName, string eventInput)
    {
      await daprClient.RaiseWorkflowEventAsync(instanceID, workflowComponent, eventName, eventInput);
      return true;
    }
  }
}
