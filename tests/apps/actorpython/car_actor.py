# -*- coding: utf-8 -*-
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.

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
