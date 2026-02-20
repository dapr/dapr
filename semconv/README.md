# Dapr Semantic Conventions

This directory contains Dapr-specific semantic conventions for OpenTelemetry, defined using the [OTel Weaver](https://github.com/open-telemetry/weaver) format.

## Structure

```
semconv/
├── model/
│   └── registry.yaml          # Semantic convention definitions
├── templates/
│   └── registry/
│       └── go/
│           ├── weaver.yaml    # Weaver configuration
│           └── workflow.go.j2 # Go code template
└── go/
    └── workflow.go            # Generated Go code
```

## Regenerating

Prerequisites:
- [OTel Weaver](https://github.com/open-telemetry/weaver) installed (`brew install weaver` or see weaver docs)

To regenerate the Go code after modifying `model/registry.yaml`:

```bash
cd semconv
weaver registry generate go --registry model --templates templates --skip-policies
mv output/workflow.go go/workflow.go
rmdir output
```

Then copy to durabletask-go:

```bash
cp go/workflow.go ../../../durabletask-go/api/semconv/workflow.go
```

## Attributes

All attributes follow the `dapr.workflow.*` namespace:

| Attribute | Type | Description |
|-----------|------|-------------|
| `dapr.workflow.type` | enum | orchestration, activity, timer |
| `dapr.workflow.name` | string | Workflow or activity name |
| `dapr.workflow.instance_id` | string | Workflow instance ID |
| `dapr.workflow.task_id` | int | Activity task ID |
| `dapr.workflow.version` | string | Workflow version |
| `dapr.workflow.status` | enum | Runtime status |
| `dapr.workflow.timer.fire_at` | string | Timer fire timestamp (ISO 8601) |
| `dapr.workflow.timer.id` | int | Timer ID |