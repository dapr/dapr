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

{{/*
Determine whether to render the placement StatefulSet's raft-log
volumeClaimTemplate and its container volumeMount.

StatefulSet.spec.volumeClaimTemplates is immutable in Kubernetes. Rendering
it unconditionally would break `helm upgrade` for any release that was
originally installed (HA disabled) before this chart made the raft-log PVC
independent of HA, the same failure this chart is meant to avoid. Look up
the live StatefulSet: if it already has a raft-log claim template, keep
rendering it; if it exists without one, keep omitting it so the upgrade
doesn't add an immutable field. Falls back to rendering when there is no
existing StatefulSet (fresh install) or when lookup() returns nothing (e.g.
offline `helm template`).
*/}}
{{- define "dapr_placement.renderRaftLogVolume" -}}
{{- $render := false -}}
{{- if eq .Values.cluster.forceInMemoryLog false -}}
  {{- $existing := lookup "apps/v1" "StatefulSet" .Release.Namespace "dapr-placement-server" -}}
  {{- if $existing -}}
    {{- range $existing.spec.volumeClaimTemplates -}}
      {{- if eq .metadata.name "raft-log" -}}
        {{- $render = true -}}
      {{- end -}}
    {{- end -}}
  {{- else -}}
    {{- $render = true -}}
  {{- end -}}
{{- end -}}
{{- if $render -}}true{{- end -}}
{{- end -}}
