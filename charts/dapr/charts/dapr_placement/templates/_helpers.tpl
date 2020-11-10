{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "dapr_placement.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "dapr_placement.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "dapr_placement.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create initial cluster peer list.
*/}}
{{- define "dapr_placement.initialcluster" -}}
{{- print "dapr-placement-server-0=dapr-placement-server-0.dapr-placement-server." .Release.Namespace ".svc" .Values.global.dnsSuffix ":" .Values.ports.raftRPCPort ",dapr-placement-server-1=dapr-placement-server-1.dapr-placement-server." .Release.Namespace ".svc" .Values.global.dnsSuffix ":" .Values.ports.raftRPCPort ",dapr-placement-server-2=dapr-placement-server-2.dapr-placement-server." .Release.Namespace ".svc" .Values.global.dnsSuffix ":" .Values.ports.raftRPCPort -}}
{{- end -}}
