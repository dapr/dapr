// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

namespace DaprDemoActor
{
  using System.Threading.Tasks;
  using Dapr.Actors;
  using System.Text.Json.Serialization;

  public interface ICarActor : IActor
  {
    Task<int> IncrementAndGetAsync(int delta);
    Task<string> CarToJSONAsync(Car car);
    Task<Car> CarFromJSONAsync(string content);
  }

  public class Car
  {
    [JsonPropertyName("vin")]
    public string Vin { get; set; }
    [JsonPropertyName("maker")]
    public string Maker { get; set; }
    [JsonPropertyName("model")]
    public string Model { get; set; }
    [JsonPropertyName("trim")]
    public string Trim { get; set; }
    [JsonPropertyName("modelYear")]
    public int ModelYear { get; set; }
    [JsonPropertyName("buildYear")]
    public int BuildYear { get; set; }
    [JsonPropertyName("photo")]
    public byte[] Photo { get; set; }
  }
}
