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

import json

from dapr.actor import Actor
from car_actor_interface import CarActorInterface


class PythonCarActor(Actor, CarActorInterface):

    def __init__(self, ctx, actor_id):
        super(PythonCarActor, self).__init__(ctx, actor_id)
        self._counter = 0

    async def increment_and_get(self, delta) -> int:
        self._counter = self._counter + delta
        return self._counter

    async def car_from_json(self, json_content) -> object:
        return json.loads(json_content)

    async def car_to_json(self, car) -> str:
        return json.dumps(car)
