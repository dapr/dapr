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
Create initial cluster peer list dynamically based on replicaCount.
*/}}
{{- define "dapr_scheduler.initialcluster" -}}
{{- $etcdClientPorts := "" -}}
{{- $initialCluster := "" -}}
{{- $namespace := .Release.Namespace -}}
{{- $replicaCount := include "dapr_scheduler.get-replicas" . | int -}}
{{- range $i, $e := until $replicaCount -}}
{{- $instanceHost := printf "dapr-scheduler-server-%d" $i -}}
{{- $instanceName := printf "%s-%s" $instanceHost (randAlphaNum 6) -}}
{{- $svcName := printf "%s.dapr-scheduler-server.%s.svc.cluster.local" $instanceHost $namespace -}}
{{- $peer := printf "%s=https://%s:%d" $instanceName $svcName (int $.Values.ports.etcdGRPCPeerPort) -}}
{{- $clientPort := int $.Values.ports.etcdGRPCClientPort -}}
{{- $instancePortPair := printf "%s=%d" $instanceName $clientPort -}}
{{- if gt $i 0 -}}
{{- $etcdClientPorts = printf "%s,%s" $etcdClientPorts $instancePortPair -}}
{{- else -}}
{{- $etcdClientPorts = $instancePortPair -}}
{{- end -}}
{{- $instancePortPair := printf "%s=%d" $instanceName $clientPort -}}
{{- $initialCluster = printf "%s%s" $initialCluster $peer -}}
{{- if ne (int $i) (sub $replicaCount 1) -}}
{{- $initialCluster = printf "%s," $initialCluster -}}
{{- end -}}
{{- end -}}
{{- printf "- \"--initial-cluster\"\n" }}
{{- printf "- \"%s\"\n" $initialCluster | indent 8 }}
{{- printf "- \"--etcd-client-ports\"\n" }}
{{- printf "- \"%s\"" $etcdClientPorts | indent 8 }}
{{- end -}}

{{/*
Gets the number of replicas.
- If `global.scheduler.enabled` is false, replicas = 0.
- If `global.ha.enabled` is true:
  - If `global.scheduler.enabled` is true, replicas = 3.
- If `global.ha.enabled` is false:
  - If `dapr_scheduler.ha` is true and `global.scheduler.enabled` is true, replicas = 3.
  - If `dapr_scheduler.ha` is false and `global.scheduler.enabled` is true, replicas = 1.
*/}}
{{- define "dapr_scheduler.get-replicas" -}}
{{-   $replicas := 0 }}
{{-   if (eq true .Values.global.scheduler.enabled) }}
{{-     if eq true .Values.global.ha.enabled }}
{{-         $replicas = 3 }}
{{-     else if eq true .Values.ha }}
{{-         $replicas = 3 }}
{{-     else }}
{{-         $replicas = 1 }}
{{-     end }}
{{-   end }}
{{-   $replicas }}
{{- end -}}
