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
