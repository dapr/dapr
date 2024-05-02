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
{{- $replicaCount := int .Values.replicaCount -}}
{{- range $i, $e := until $replicaCount -}}
{{- $instanceName := printf "dapr-scheduler-server-%d" $i -}}
{{- $svcName := printf "%s.dapr-scheduler-server.%s.svc.cluster.local" $instanceName $namespace -}}
{{- $peer := printf "%s=http://%s:%d" $instanceName $svcName (int $.Values.ports.etcdRPCPeerPort) -}}
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
{{- $replicaCount := int .Values.replicaCount -}}
{{- range $i, $e := until $replicaCount -}}
{{- $instanceName := printf "dapr-scheduler-server-%d" $i -}}
{{/*{{- $svcName := printf "%s.%s" $instanceName $namespace -}}*/}}
{{- $clientPort := int $.Values.ports.etcdRPCClientPort -}}
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
{{- $replicaCount := int .Values.replicaCount -}}
{{- range $i, $e := until $replicaCount -}}
{{- $instanceName := printf "dapr-scheduler-server-%d" $i -}}
{{/*{{- $svcName := printf "%s.%s" $instanceName $namespace -}}*/}}
{{- $clientPort := int $.Values.ports.etcdHttpClientPort -}}
{{- $instancePortPair := printf "%s=%d" $instanceName $clientPort -}}
{{- if gt $i 0 -}}
{{- $etcdClientHttpPorts = printf "%s,%s" $etcdClientHttpPorts $instancePortPair -}}
{{- else -}}
{{- $etcdClientHttpPorts = $instancePortPair -}}
{{- end -}}
{{- end -}}
{{- $etcdClientHttpPorts -}}
{{- end -}}


