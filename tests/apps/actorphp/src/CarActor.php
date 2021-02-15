<?php

namespace Test;

use Dapr\Actors\Actor;
use Dapr\Actors\Attributes\DaprType;
use Dapr\Deserialization\IDeserializer;
use Dapr\Serialization\ISerializer;
use Dapr\Serialization\Serializer;

#[DaprType('PHPCarActor')]
class CarActor extends Actor implements ICarActor
{

    private int $count = 0;

    public function __construct(
        public string $id,
        protected ISerializer $serializer,
        protected IDeserializer $deserializer
    ) {
        parent::__construct($id);
    }

    public function IncrementAndGetAsync(int|null $delta): int
    {
        $this->count += $delta;

        return $this->count;
    }

    public function CarFromJSONAsync(string $content): Car
    {
        return $this->deserializer->from_json(Car::class, $content);
    }

    public function CarToJSONAsync(Car $car): string
    {
        return $this->serializer->as_json($car);
    }
}
