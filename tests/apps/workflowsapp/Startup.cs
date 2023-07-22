// ------------------------------------------------------------------------
// Copyright 2021 The Dapr Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// ------------------------------------------------------------------------

namespace DaprDemoActor
{
    using Dapr.Workflow;
    using Microsoft.AspNetCore.Authentication;
    using Microsoft.AspNetCore.Authorization;
    using Microsoft.AspNetCore.Builder;
    using Microsoft.AspNetCore.Hosting;
    using Microsoft.Extensions.Configuration;
    using Microsoft.Extensions.DependencyInjection;
    using Microsoft.Extensions.Hosting;
    using System.Threading.Tasks;
    using System;

    /// <summary>
    /// Startup class.
    /// </summary>
    public class Startup
    {
        /// <summary>
        /// Initializes a new instance of the <see cref="Startup"/> class.
        /// </summary>
        /// <param name="configuration">Configuration.</param>
        public Startup(IConfiguration configuration)
        {
            this.Configuration = configuration;
        }

        /// <summary>
        /// Gets the configuration.
        /// </summary>
        public IConfiguration Configuration { get; }

        /// <summary>
        /// Configures Services.
        /// </summary>
        /// <param name="services">Service Collection.</param>
        public void ConfigureServices(IServiceCollection services)
        {
            services.AddDaprWorkflow(options =>
            {
                // Example of registering a "PlaceOrder" workflow function
                options.RegisterWorkflow<string, string>("PlaceOrder", implementation: async (context, input) =>
                {

                    var itemToPurchase = input;

                    itemToPurchase = await context.WaitForExternalEventAsync<string>("ChangePurchaseItem");

                    // In real life there are other steps related to placing an order, like reserving
                    // inventory and charging the customer credit card etc. But let's keep it simple ;)
                    await context.CallActivityAsync<string>("ShipProduct", itemToPurchase);

                    return itemToPurchase;
                });
                // Example of registering a "ShipProduct" workflow activity function
                options.RegisterActivity<string, string>("ShipProduct", implementation: (context, input) =>
                {
                    return Task.FromResult($"We are shipping {input} to the customer using our hoard of drones!");
                });

            });
            services.AddAuthentication().AddDapr();
            services.AddAuthorization(o => o.AddDapr());
            services.AddControllers().AddDapr();
        }

        /// <summary>
        /// Configures Application Builder and WebHost environment.
        /// </summary>
        /// <param name="app">Application builder.</param>
        /// <param name="env">Webhost environment.</param>
        public void Configure(IApplicationBuilder app, IWebHostEnvironment env)
        {
            if (env.IsDevelopment())
            {
                app.UseDeveloperExceptionPage();
            }

            app.UseRouting();

            app.UseAuthentication();

            app.UseAuthorization();

            app.UseCloudEvents();

            app.UseEndpoints(endpoints =>
            {
                endpoints.MapSubscribeHandler();
                endpoints.MapControllers();
            });
        }
    }
}
