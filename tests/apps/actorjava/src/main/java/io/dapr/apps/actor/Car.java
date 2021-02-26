package io.dapr.apps.actor;
/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */

import lombok.Data;
import lombok.NoArgsConstructor;

@Data
@NoArgsConstructor
public class Car {
  private String vin;
  private String maker;
  private String model;
  private String trim;
  private int modelYear;
  private int buildYear;
  byte[] photo;
}
