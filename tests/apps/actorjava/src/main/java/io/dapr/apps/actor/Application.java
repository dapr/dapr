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

package io.dapr.apps.actor;

import java.time.Duration;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

import io.dapr.actors.runtime.ActorRuntime;
import io.dapr.apps.actor.actors.CarActorImpl;

@SpringBootApplication(scanBasePackages = { "io.dapr.apps.actor", "io.dapr.springboot"})
public class Application {

    public static void main(String[] args) {
        ActorRuntime.getInstance().getConfig().setActorIdleTimeout(Duration.ofSeconds(1));
        ActorRuntime.getInstance().getConfig().setActorScanInterval(Duration.ofSeconds(1));
        ActorRuntime.getInstance().registerActor(CarActorImpl.class);
        SpringApplication.run(Application.class, args);
    }

}
