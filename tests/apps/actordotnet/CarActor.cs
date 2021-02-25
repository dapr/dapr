// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

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
