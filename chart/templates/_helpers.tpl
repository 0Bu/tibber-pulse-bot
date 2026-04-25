{{- define "tibber-pulse-bot.fullname" -}}
{{- default .Release.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "tibber-pulse-bot.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "tibber-pulse-bot.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "tibber-pulse-bot.secretName" -}}
{{- if .Values.pulse.existingSecret -}}
{{- .Values.pulse.existingSecret -}}
{{- else -}}
{{- include "tibber-pulse-bot.fullname" . -}}
{{- end -}}
{{- end -}}
