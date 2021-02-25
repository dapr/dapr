// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

namespace DaprDemoActor
{
  using Dapr.Actors;
  using Dapr.Actors.Client;
  using Microsoft.AspNetCore.Mvc;
  using System.IO;
  using System.Threading.Tasks;
  using System.Text;

  [ApiController]
  [Route("/")]
  public class Controller : ControllerBase
  {
    [HttpPost("incrementAndGet/{actorType}/{actorId}")]
    public async Task<ActionResult<int>> IncrementAndGetAsync([FromRoute] string actorType, [FromRoute] string actorId)
    {
      var proxy = ActorProxy.Create(new ActorId(actorId), actorType);
      return await proxy.InvokeAsync<int, int>("IncrementAndGetAsync", 1);
    }

    [HttpPost("carFromJSON/{actorType}/{actorId}")]
    public async Task<ActionResult<Car>> CarFromJSONAsync([FromRoute] string actorType, [FromRoute] string actorId)
    {
      using (var reader = new StreamReader(Request.Body, Encoding.UTF8))
      {
        string json = await reader.ReadToEndAsync();
        var proxy = ActorProxy.Create(new ActorId(actorId), actorType);
        return await proxy.InvokeAsync<string, Car>("CarFromJSONAsync", json);
      }
    }

    [HttpPost("carToJSON/{actorType}/{actorId}")]
    public async Task<ActionResult<string>> CarToJSONAsync([FromRoute] string actorType, [FromRoute] string actorId, [FromBody] Car car)
    {
      var proxy = ActorProxy.Create(new ActorId(actorId), actorType);
      return await proxy.InvokeAsync<Car, string>("CarToJSONAsync", car);
    }
  }
}
