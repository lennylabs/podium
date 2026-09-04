{{- define "podium.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "podium.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "podium.labels" -}}
app.kubernetes.io/name: {{ include "podium.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "podium.selectorLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "podium.pgFullname" -}}
{{- if .Values.postgresql.fullnameOverride -}}
{{- .Values.postgresql.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-pg" (include "podium.fullname" . | trunc 60 | trimSuffix "-") -}}
{{- end -}}
{{- end -}}

{{- define "podium.pgSelectorLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . | trunc 60 | trimSuffix "-" }}-pg
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "podium.pgLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . | trunc 60 | trimSuffix "-" }}-pg
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: postgresql
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}
