{{/*
Expand the name of the chart.
*/}}
{{- define "dapr_scheduler.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "dapr_scheduler.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Gets the number of replicas.
- If `global.scheduler.enabled` is false, replicas = 0.
- If `global.ha.enabled` is true, replicas = 3.
*/}}
{{- define "dapr_scheduler.get-replicas" -}}
{{-   $replicas := 0 }}
{{-   if (eq true .Values.global.scheduler.enabled) }}
{{-         $replicas = 3 }}
{{-    end -}}
{{-   $replicas }}
{{- end -}}
