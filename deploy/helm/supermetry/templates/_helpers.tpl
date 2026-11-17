{{/*
Chart name, truncated to the 63 characters Kubernetes allows in names.
*/}}
{{- define "supermetry.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name. Truncated to leave room for the "-postgresql" suffix.
*/}}
{{- define "supermetry.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 52 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 52 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 52 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "supermetry.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "supermetry.labels" -}}
helm.sh/chart: {{ include "supermetry.chart" . }}
{{ include "supermetry.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "supermetry.selectorLabels" -}}
app.kubernetes.io/name: {{ include "supermetry.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "supermetry.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "supermetry.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "supermetry.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}
{{- end }}

{{/*
Name of the Secret the chart creates (secret key, database URL, PostgreSQL password).
*/}}
{{- define "supermetry.secretName" -}}
{{- include "supermetry.fullname" . }}
{{- end }}

{{- define "supermetry.secretKeySecretName" -}}
{{- default (include "supermetry.secretName" .) .Values.secretKey.existingSecret }}
{{- end }}

{{- define "supermetry.secretKeySecretKey" -}}
{{- if .Values.secretKey.existingSecret }}{{ .Values.secretKey.existingSecretKey }}{{ else }}secret-key{{ end }}
{{- end }}

{{- define "supermetry.postgresql.fullname" -}}
{{- printf "%s-postgresql" (include "supermetry.fullname" .) }}
{{- end }}

{{- define "supermetry.postgresql.selectorLabels" -}}
app.kubernetes.io/name: {{ include "supermetry.name" . }}-postgresql
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "supermetry.postgresql.labels" -}}
helm.sh/chart: {{ include "supermetry.chart" . }}
{{ include "supermetry.postgresql.selectorLabels" . }}
app.kubernetes.io/component: database
app.kubernetes.io/part-of: {{ include "supermetry.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "supermetry.postgresql.secretName" -}}
{{- default (include "supermetry.secretName" .) .Values.postgresql.auth.existingSecret }}
{{- end }}

{{- define "supermetry.postgresql.secretKey" -}}
{{- if .Values.postgresql.auth.existingSecret }}{{ .Values.postgresql.auth.existingSecretPasswordKey }}{{ else }}postgres-password{{ end }}
{{- end }}

{{- define "supermetry.databaseUrlSecretName" -}}
{{- default (include "supermetry.secretName" .) .Values.externalDatabase.existingSecret }}
{{- end }}

{{- define "supermetry.databaseUrlSecretKey" -}}
{{- if .Values.externalDatabase.existingSecret }}{{ .Values.externalDatabase.existingSecretKey }}{{ else }}database-url{{ end }}
{{- end }}

{{/*
True when the chart must create its own Secret.
*/}}
{{- define "supermetry.createSecret" -}}
{{- if or (not .Values.secretKey.existingSecret) (and .Values.postgresql.enabled (not .Values.postgresql.auth.existingSecret)) (and (not .Values.postgresql.enabled) (not .Values.externalDatabase.existingSecret)) -}}
true
{{- end }}
{{- end }}

{{/*
SUPERMETRY_BASE_URL: explicit value, else the first ingress host, else localhost.
*/}}
{{- define "supermetry.baseUrl" -}}
{{- if .Values.config.baseUrl }}
{{- .Values.config.baseUrl | trimSuffix "/" }}
{{- else if and .Values.ingress.enabled .Values.ingress.hosts }}
{{- $host := (first .Values.ingress.hosts).host }}
{{- $scheme := "http" }}
{{- range .Values.ingress.tls }}
{{- if has $host (default list .hosts) }}{{ $scheme = "https" }}{{ end }}
{{- end }}
{{- printf "%s://%s" $scheme $host }}
{{- else }}
{{- printf "http://localhost:%v" .Values.service.port }}
{{- end }}
{{- end }}

{{/*
Fail early on settings that cannot work.
*/}}
{{- define "supermetry.validate" -}}
{{- if ne (int .Values.replicaCount) 1 }}
{{- fail "replicaCount must be 1: Supermetry keeps live sessions in process memory, so it cannot be scaled horizontally" }}
{{- end }}
{{- if and (not .Values.postgresql.enabled) (not .Values.externalDatabase.url) (not .Values.externalDatabase.existingSecret) }}
{{- fail "set externalDatabase.url or externalDatabase.existingSecret, or enable the bundled database with postgresql.enabled=true" }}
{{- end }}
{{- if and (not .Values.postgresql.enabled) .Values.externalDatabase.url .Values.externalDatabase.existingSecret }}
{{- fail "set only one of externalDatabase.url and externalDatabase.existingSecret" }}
{{- end }}
{{- if .Values.secretKey.value }}
{{- $decoded := b64dec .Values.secretKey.value }}
{{- if lt (len $decoded) 32 }}
{{- fail "secretKey.value must be base64 of at least 32 bytes (openssl rand -base64 32)" }}
{{- end }}
{{- end }}
{{- end }}
