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
package io.dapr.apps.actor.actors;

import java.io.IOException;
import java.util.concurrent.atomic.AtomicInteger;

import com.fasterxml.jackson.databind.ObjectMapper;

import io.dapr.actors.ActorId;
import io.dapr.actors.runtime.AbstractActor;
import io.dapr.actors.runtime.ActorRuntimeContext;
import io.dapr.apps.actor.Car;


public class CarActorImpl extends AbstractActor implements CarActor {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private final AtomicInteger counter = new AtomicInteger();

    /**
     * This is the constructor of an actor implementation.
     *
     * @param runtimeContext The runtime context object which contains objects such
     *                       as the state provider.
     * @param id             The id of this actor.
     */
    public CarActorImpl(ActorRuntimeContext<?> runtimeContext, ActorId id) {
        super(runtimeContext, id);
    }

    @Override
    public int IncrementAndGetAsync(int delta) {
        return this.counter.addAndGet(delta);
    }

    @Override
    public String CarToJSONAsync(Car car) throws IOException {
        if (car == null) {
            return "";
        }
        return OBJECT_MAPPER.writeValueAsString(car);
    }

    @Override
    public Car CarFromJSONAsync(String content) throws IOException {
        return OBJECT_MAPPER.readValue(content, Car.class);
    }
}
