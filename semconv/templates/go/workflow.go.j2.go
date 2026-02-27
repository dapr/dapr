// Copyright 2025 The Dapr Authors
// SPDX-License-Identifier: Apache-2.0

// Code generated from semantic convention specification. DO NOT EDIT.

package semconv

import "go.opentelemetry.io/otel/attribute"

{% for group in ctx.groups %}
// {{ group.brief }}
const (
{%- for attr in group.attributes %}
// {{ attr.name | pascal_case }}Key is the attribute Key conforming to the "{{ attr.name }}" semantic conventions.
// {{ attr.brief }}
{{ attr.name | pascal_case }}Key = attribute.Key("{{ attr.name }}")
{%- endfor %}
)
{% endfor %}