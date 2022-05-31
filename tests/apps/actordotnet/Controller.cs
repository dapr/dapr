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
