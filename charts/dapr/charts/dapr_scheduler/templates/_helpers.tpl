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
{{- $initialCluster := "" -}}
{{- $namespace := .Release.Namespace -}}
{{- $replicaCount := include "dapr_scheduler.get-replicas" . | int -}}
{{- range $i, $e := until $replicaCount -}}
{{- $instanceName := printf "dapr-scheduler-server-%d" $i -}}
{{- $svcName := printf "%s.dapr-scheduler-server.%s.svc.cluster.local" $instanceName $namespace -}}
{{- $peer := printf "%s=http://%s:%d" $instanceName $svcName (int $.Values.ports.etcdGRPCPeerPort) -}}
{{- $initialCluster = printf "%s%s" $initialCluster $peer -}}
{{- if ne (int $i) (sub $replicaCount 1) -}}
{{- $initialCluster = printf "%s," $initialCluster -}}
{{- end -}}
{{- end -}}
{{- $initialCluster -}}
{{- end -}}

{{/*
Create etcd client ports list dynamically based on replicaCount.
*/}}
{{- define "dapr_scheduler.etcdclientports" -}}
{{- $etcdClientPorts := "" -}}
{{- $namespace := .Release.Namespace -}}
{{- $replicaCount := include "dapr_scheduler.get-replicas" . | int -}}
{{- range $i, $e := until $replicaCount -}}
{{- $instanceName := printf "dapr-scheduler-server-%d" $i -}}
{{- $clientPort := int $.Values.ports.etcdGRPCClientPort -}}
{{- $instancePortPair := printf "%s=%d" $instanceName $clientPort -}}
{{- if gt $i 0 -}}
{{- $etcdClientPorts = printf "%s,%s" $etcdClientPorts $instancePortPair -}}
{{- else -}}
{{- $etcdClientPorts = $instancePortPair -}}
{{- end -}}
{{- end -}}
{{- $etcdClientPorts -}}
{{- end -}}

{{/*
Create etcd client http ports list dynamically based on replicaCount.
*/}}
{{- define "dapr_scheduler.etcdclienthttpports" -}}
{{- $etcdClientHttpPorts := "" -}}
{{- $namespace := .Release.Namespace -}}
{{- $replicaCount := include "dapr_scheduler.get-replicas" . | int -}}
{{- range $i, $e := until $replicaCount -}}
{{- $instanceName := printf "dapr-scheduler-server-%d" $i -}}
{{- $clientPort := int $.Values.ports.etcdHTTPClientPort -}}
{{- $instancePortPair := printf "%s=%d" $instanceName $clientPort -}}
{{- if gt $i 0 -}}
{{- $etcdClientHttpPorts = printf "%s,%s" $etcdClientHttpPorts $instancePortPair -}}
{{- else -}}
{{- $etcdClientHttpPorts = $instancePortPair -}}
{{- end -}}
{{- end -}}
{{- $etcdClientHttpPorts -}}
{{- end -}}

{{/*
Gets the number of replicas. If global.ha.enabled is true, then 3. Otherwise, 1.
*/}}
{{- define "dapr_scheduler.get-replicas" -}}
{{-   $replicas := 0 }}
{{-   if and (eq true .Values.global.ha.enabled) (eq .Values.global.scheduler.enabled true) }}
{{-       $replicas = 3 }}
{{-   else if and (eq false .Values.global.ha.enabled) (eq .Values.global.scheduler.enabled true) -}}
{{-       $replicas = 1 }}
{{-   end }}
{{-   $replicas }}
{{- end -}}
