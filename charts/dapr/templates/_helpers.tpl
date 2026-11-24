{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "k8s_operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "k8s_operator.fullname" -}}
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
{{- define "k8s_operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Formats imagePullSecrets. Input is dict( "imagePullSecrets" .{specific imagePullSecrets}).
*/}}
{{- define "dapr.imagePullSecrets" -}}
{{- if eq (typeOf .imagePullSecrets) "string" -}} {{- /* Single string value */ -}}
- name: {{ .imagePullSecrets }}
{{- else -}} {{- /* Not a string value, iterate */ -}}
{{- range .imagePullSecrets -}}
{{- if eq (typeOf .) "map[string]interface {}" -}} {{- /* k8s style */ -}}
- {{ toYaml (dict "name" .name) | trim }}
{{ else }} {{- /* helm style */ -}}
- name: {{ . }}
{{ end }} {{- /* End of inner if */ -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Validates podAntiAffinityPolicy is one of the allowed values.
*/}}
{{- define "dapr.validatePodAntiAffinityPolicy" -}}
{{- if not (or (eq .Values.global.ha.podAntiAffinityPolicy "preferredDuringSchedulingIgnoredDuringExecution") (eq .Values.global.ha.podAntiAffinityPolicy "requiredDuringSchedulingIgnoredDuringExecution")) -}}
{{- fail "global.ha.podAntiAffinityPolicy must be either 'preferredDuringSchedulingIgnoredDuringExecution' or 'requiredDuringSchedulingIgnoredDuringExecution'" -}}
{{- end -}}
{{- end -}}

{{/*
Returns "true" when actor placement is served by the Scheduler, else "false".
global.scheduler.placement is absent from values of releases installed before
1.19, which helm upgrade --reuse-values renders without the new chart defaults.
*/}}
{{- define "dapr.schedulerPlacementEnabled" -}}
{{- $scheduler := .Values.global.scheduler | default dict -}}
{{- $placement := $scheduler.placement | default dict -}}
{{- $placement.enabled | default false | toString -}}
{{- end -}}

{{/*
Render optional container security settings, preferring component values per field.
*/}}
{{- define "dapr.containerSecurityContext" -}}
{{- $global := .Values.global.securityContext | default dict -}}
{{- $component := .Values.securityContext | default dict -}}
{{- $context := dict -}}
{{- range $key := list "runAsUser" "runAsGroup" "seLinuxOptions" -}}
  {{- $value := index $global $key -}}
  {{- if hasKey $component $key -}}
    {{- $value = index $component $key -}}
  {{- end -}}
  {{- if not (kindIs "invalid" $value) -}}
    {{- $_ := set $context $key $value -}}
  {{- end -}}
{{- end -}}
{{- with $context -}}
  {{- toYaml . -}}
{{- end -}}
{{- end -}}

{{/*
Apply the fsGroup default at render time so Helm can preserve an explicit null.
*/}}
{{- define "dapr.podFSGroup" -}}
{{- $fsGroup := 65532 -}}
{{- if hasKey . "fsGroup" -}}
  {{- $fsGroup = .fsGroup -}}
{{- end -}}
{{- if not (kindIs "invalid" $fsGroup) -}}
securityContext:
  fsGroup: {{ $fsGroup }}
{{- end -}}
{{- end -}}
