{{/*
SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
SPDX-License-Identifier: Apache-2.0
*/}}
{{/* Fully qualified name. Defaults to a stable name so the service host is predictable. */}}
{{- define "postgres.fullname" -}}
{{- default (printf "%s-postgres" .Release.Name) .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Chart label. */}}
{{- define "postgres.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "postgres.labels" -}}
helm.sh/chart: {{ include "postgres.chart" . }}
{{ include "postgres.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Selector labels. */}}
{{- define "postgres.selectorLabels" -}}
app.kubernetes.io/name: postgres
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Name of the Secret holding the database password. */}}
{{- define "postgres.secretName" -}}
{{- if .Values.auth.existingSecret -}}
{{- .Values.auth.existingSecret -}}
{{- else -}}
{{- include "postgres.fullname" . -}}
{{- end -}}
{{- end -}}
