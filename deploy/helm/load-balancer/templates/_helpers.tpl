{{- define "go-load-balancer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "go-load-balancer.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "go-load-balancer.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "go-load-balancer.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "go-load-balancer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "go-load-balancer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "go-load-balancer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: data-plane
{{- end }}

{{- define "go-load-balancer.consoleSelectorLabels" -}}
app.kubernetes.io/name: {{ include "go-load-balancer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: console
{{- end }}

{{- define "go-load-balancer.image" -}}
{{- if .digest -}}
{{- printf "%s@%s" .repository .digest -}}
{{- else -}}
{{- printf "%s:%s" .repository .tag -}}
{{- end -}}
{{- end }}

{{- define "go-load-balancer.discoveryServiceAccount" -}}
{{- printf "%s-discovery" (include "go-load-balancer.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
