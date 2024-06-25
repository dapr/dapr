{{/*
Returns the address and port of the scheduler service
The returned value is a string in the format "<name>:<port>"
*/}}
{{- define "address.scheduler" -}}
{{- "dapr-scheduler-server:50006" }}
{{- end -}}