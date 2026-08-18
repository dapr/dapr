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
Resolve the PVC storage size for the scheduler StatefulSet.

StatefulSet.spec.volumeClaimTemplates is immutable in Kubernetes, so we cannot
change the requested PVC size on an existing release. To let new installs
default to a larger size without breaking helm upgrade on existing clusters,
look up the live StatefulSet and pin storage to the value already in use when
one exists. Falls back to .Values.cluster.storageSize for fresh installs and
when lookup() returns nothing (e.g. offline `helm template`).
*/}}
{{- define "dapr_scheduler.storageSize" -}}
{{- $storageSize := .Values.cluster.storageSize -}}
{{- $existing := lookup "apps/v1" "StatefulSet" .Release.Namespace "dapr-scheduler-server" -}}
{{- if $existing -}}
{{- range $existing.spec.volumeClaimTemplates -}}
{{- if eq .metadata.name "dapr-scheduler-data-dir" -}}
{{- $storageSize = .spec.resources.requests.storage -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $storageSize -}}
{{- end -}}
