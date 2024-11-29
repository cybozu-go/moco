{{/*
Expand the name of the chart.
*/}}
{{- define "moco.name" -}}
{{- default .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "moco.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "moco.labels" -}}
helm.sh/chart: {{ include "moco.chart" . }}
app.kubernetes.io/name: {{ include "moco.name" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Return the appropriate apiVersion for admissionregistration.
*/}}
{{- define "admissionregistration.apiVersion" -}}
{{- if (lt (int .Capabilities.KubeVersion.Minor) 30) -}}
admissionregistration.k8s.io/v1beta1
{{- else -}}
admissionregistration.k8s.io/v1
{{- end }}
{{- end }}
