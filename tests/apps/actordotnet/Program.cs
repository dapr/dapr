// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

namespace DaprDemoActor
{
  using Dapr.Actors.AspNetCore;
  using Microsoft.AspNetCore.Hosting;
  using Microsoft.Extensions.Hosting;

  public class Program
  {
    public static void Main(string[] args)
    {
      CreateHostBuilder(args).Build().Run();
    }

    public static IHostBuilder CreateHostBuilder(string[] args) =>
        Host.CreateDefaultBuilder(args)
            .ConfigureWebHostDefaults(webBuilder =>
            {
              webBuilder.UseStartup<Startup>().UseActors(options =>
              {
                options.Actors.RegisterActor<CarActor>();
              })
              .UseUrls("http://*:3000");
            });
  }
}
