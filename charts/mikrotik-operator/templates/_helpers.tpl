{{- define "mikrotik-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "mikrotik-operator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "mikrotik-operator.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "mikrotik-operator.labels" -}}
app.kubernetes.io/name: {{ include "mikrotik-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{- define "mikrotik-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "mikrotik-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "mikrotik-operator.ui.name" -}}
{{- printf "%s-ui" (include "mikrotik-operator.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "mikrotik-operator.ui.fullname" -}}
{{- printf "%s-ui" (include "mikrotik-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "mikrotik-operator.ui.labels" -}}
app.kubernetes.io/name: {{ include "mikrotik-operator.ui.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: ui
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{- define "mikrotik-operator.ui.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mikrotik-operator.ui.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: ui
{{- end }}

{{- define "mikrotik-operator.ui.serviceAccountName" -}}
{{- include "mikrotik-operator.ui.fullname" . }}
{{- end }}
