# -*- coding: utf-8 -*-
#
# Copyright 2021 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

from dapr.actor import ActorInterface, actormethod


class CarActorInterface(ActorInterface):
    @actormethod(name="IncrementAndGetAsync")
    async def increment_and_get(self, delta: int) -> int:
        ...

    @actormethod(name="CarFromJSONAsync")
    async def car_from_json(self, json_content: str) -> object:
        ...

    @actormethod(name="CarToJSONAsync")
    async def car_to_json(self, car: object) -> str:
        ...
