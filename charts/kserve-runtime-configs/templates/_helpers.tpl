{{/*
Expand the name of the chart.
*/}}
{{- define "kserve-runtime-configs.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kserve-runtime-configs.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kserve-runtime-configs.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kserve-runtime-configs.labels" -}}
helm.sh/chart: {{ include "kserve-runtime-configs.chart" . }}
{{ include "kserve-runtime-configs.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kserve-runtime-configs.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kserve-runtime-configs.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Deep merge two dictionaries (recursive)

Usage: include "kserve-runtime-configs.deepMerge" (list $base $patch) | fromYaml

IMPORTANT LIMITATIONS:
- Only merges dictionaries (maps), NOT arrays
- Arrays in $patch completely replace arrays in $base (no element-wise merge)
- For resources with array fields (e.g., webhooks), use string replacement instead

Example:
  Base:    {a: {b: 1, c: 2}, d: [1,2]}
  Patch:   {a: {b: 10}, d: [3]}
  Result:  {a: {b: 10, c: 2}, d: [3]}  <- d array is replaced, not merged
*/}}
{{- define "kserve-runtime-configs.deepMerge" -}}
{{- $base := index . 0 -}}
{{- $patch := index . 1 -}}
{{- range $key, $value := $patch -}}
  {{- if hasKey $base $key -}}
    {{- $baseValue := get $base $key -}}
    {{- if and (kindIs "map" $value) (kindIs "map" $baseValue) -}}
      {{- $merged := include "kserve-runtime-configs.deepMerge" (list $baseValue $value) | fromYaml -}}
      {{- $_ := set $base $key $merged -}}
    {{- else -}}
      {{- $_ := set $base $key $value -}}
    {{- end -}}
  {{- else -}}
    {{- $_ := set $base $key $value -}}
  {{- end -}}
{{- end -}}
{{ toYaml $base }}
{{- end }}

{{/*
Safe namespace replacement - only replaces exact "namespace: kserve" pattern
*/}}
{{- define "kserve-runtime-configs.replaceNamespace" -}}
{{- $content := index . 0 -}}
{{- $namespace := index . 1 -}}
{{- $pattern := "namespace: kserve\n" -}}
{{- $replacement := printf "namespace: %s\n" $namespace -}}
{{- $content | replace $pattern $replacement -}}
{{- end -}}
