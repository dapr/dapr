{{/*
Returns the address and port of the placement service
The returned value is a string in the format "<name>:<port>"
*/}}
{{- define "address.placement" -}}
{{- "dapr-placement-server:50005" }}
{{- end -}}