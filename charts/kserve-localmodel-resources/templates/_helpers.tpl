{{/*
Expand the name of the chart.
*/}}
{{- define "kserve-localmodel-resources.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kserve-localmodel-resources.fullname" -}}
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
{{- define "kserve-localmodel-resources.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kserve-localmodel-resources.labels" -}}
helm.sh/chart: {{ include "kserve-localmodel-resources.chart" . }}
{{ include "kserve-localmodel-resources.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kserve-localmodel-resources.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kserve-localmodel-resources.deploymentName" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the deployment name
*/}}
{{- define "kserve-localmodel-resources.deploymentName" -}}
kserve-localmodel-controller-manager
{{- end }}

{{/*
Deep merge two dictionaries (recursive)

Usage: include "kserve-localmodel-resources.deepMerge" (list $base $patch) | fromYaml

IMPORTANT LIMITATIONS:
- Only merges dictionaries (maps), NOT arrays
- Arrays in $patch completely replace arrays in $base (no element-wise merge)
- For resources with array fields (e.g., webhooks), use string replacement instead

Example:
  Base:    {a: {b: 1, c: 2}, d: [1,2]}
  Patch:   {a: {b: 10}, d: [3]}
  Result:  {a: {b: 10, c: 2}, d: [3]}  <- d array is replaced, not merged
*/}}
{{- define "kserve-localmodel-resources.deepMerge" -}}
{{- $base := index . 0 -}}
{{- $patch := index . 1 -}}
{{- range $key, $value := $patch -}}
  {{- if hasKey $base $key -}}
    {{- $baseValue := get $base $key -}}
    {{- if and (kindIs "map" $value) (kindIs "map" $baseValue) -}}
      {{- $merged := include "kserve-localmodel-resources.deepMerge" (list $baseValue $value) | fromYaml -}}
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
{{- define "kserve-localmodel-resources.replaceNamespace" -}}
{{- $content := index . 0 -}}
{{- $namespace := index . 1 -}}
{{- $pattern := "namespace: kserve\n" -}}
{{- $replacement := printf "namespace: %s\n" $namespace -}}
{{- $content | replace $pattern $replacement -}}
{{- end -}}
