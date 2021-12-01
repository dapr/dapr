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
  using Dapr.Actors.Runtime;
  using System.Threading.Tasks;
  using System.Text.Json;

  [Actor(TypeName = "DotNetCarActor")]
  public class CarActor : Actor, ICarActor
  {
    private int count = 0;

    public CarActor(ActorHost host) : base(host)
    {
    }

    public Task<int> IncrementAndGetAsync(int delta)
    {
      count += delta;
      return Task.FromResult(count);
    }

    public Task<string> CarToJSONAsync(Car car)
    {
      string json = JsonSerializer.Serialize(car);
      return Task.FromResult(json);
    }
    public Task<Car> CarFromJSONAsync(string content)
    {
      System.Console.WriteLine(content);
      Car car = JsonSerializer.Deserialize<Car>(content);
      return Task.FromResult(car);
    }
  }
}
