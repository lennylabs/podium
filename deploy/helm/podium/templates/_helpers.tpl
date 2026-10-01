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

{{/*
The migrate Job's name. The suffix is appended after truncating the base, so a
63-character fullnameOverride still yields a name that ends in -migrate and fits
the 63-character limit.
*/}}
{{- define "podium.migrateFullname" -}}
{{- printf "%s-migrate" (include "podium.fullname" . | trunc 55 | trimSuffix "-") -}}
{{- end -}}

{{/*
Labels for the migrate Job and its pod. The name value differs from the
selector's, so the Service never routes to the Job pod and the documented
`kubectl wait` does not match it. The component label is what the documented
cleanup selector matches.
*/}}
{{- define "podium.migrateLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . | trunc 55 | trimSuffix "-" }}-migrate
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: migrate
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/*
The registry image reference, shared by the Deployment and the migrate Job and
compared with migration.previousImage in podium.validate.
*/}}
{{- define "podium.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion | toString) -}}
{{- end -}}
