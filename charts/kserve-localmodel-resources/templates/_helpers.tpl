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

Features:
- Merges dictionaries (maps) recursively
- Arrays with named elements (e.g., containers, env, volumeMounts) are merged by name
- Other arrays in $patch completely replace arrays in $base

Example:
  Base:    {a: {b: 1, c: 2}, containers: [{name: foo, x: 1}]}
  Patch:   {a: {b: 10}, containers: [{name: foo, y: 2}]}
  Result:  {a: {b: 10, c: 2}, containers: [{name: foo, x: 1, y: 2}]}
*/}}
{{- define "kserve-localmodel-resources.deepMerge" -}}
{{- $base := index . 0 -}}
{{- $patch := index . 1 -}}
{{- range $key, $value := $patch -}}
  {{- if hasKey $base $key -}}
    {{- $baseValue := get $base $key -}}
    {{- if and (kindIs "map" $value) (kindIs "map" $baseValue) -}}
      {{- /* Recursively merge nested maps */ -}}
      {{- $merged := include "kserve-localmodel-resources.deepMerge" (list $baseValue $value) | fromYaml -}}
      {{- $_ := set $base $key $merged -}}
    {{- else if and (kindIs "slice" $value) (kindIs "slice" $baseValue) -}}
      {{- /* Check if array elements have 'name' field for smart merge */ -}}
      {{- $canMergeByName := false -}}
      {{- if gt (len $value) 0 -}}
        {{- $firstElem := index $value 0 -}}
        {{- if and (kindIs "map" $firstElem) (hasKey $firstElem "name") -}}
          {{- $canMergeByName = true -}}
        {{- end -}}
      {{- end -}}
      {{- if $canMergeByName -}}
        {{- /* Merge arrays by name */ -}}
        {{- $mergedResult := include "kserve-localmodel-resources.mergeArrayByName" (list $baseValue $value) | fromYaml -}}
        {{- $_ := set $base $key (get $mergedResult "items") -}}
      {{- else -}}
        {{- /* Replace array completely */ -}}
        {{- $_ := set $base $key $value -}}
      {{- end -}}
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
Merge two arrays by matching 'name' field

Usage: include "kserve-localmodel-resources.mergeArrayByName" (list $baseArray $patchArray) | fromYaml

For each item in patch array:
- If matching name exists in base, merge them (recursively)
- Otherwise add as new item
Items in base without matching patch are preserved
*/}}
{{- define "kserve-localmodel-resources.mergeArrayByName" -}}
{{- $baseArray := index . 0 -}}
{{- $patchArray := index . 1 -}}
{{- $processedNames := dict -}}
{{- $result := dict "items" (list) -}}

{{- /* First pass: merge matching items from base with patches */ -}}
{{- range $baseItem := $baseArray -}}
  {{- $name := $baseItem.name -}}
  {{- $matched := false -}}
  {{- range $patchItem := $patchArray -}}
    {{- if eq $patchItem.name $name -}}
      {{- /* Found matching item - merge them */ -}}
      {{- $merged := include "kserve-localmodel-resources.deepMerge" (list $baseItem $patchItem) | fromYaml -}}
      {{- $_ := set $result "items" (append (get $result "items") $merged) -}}
      {{- $_ := set $processedNames $name true -}}
      {{- $matched = true -}}
    {{- end -}}
  {{- end -}}
  {{- if not $matched -}}
    {{- /* No patch for this base item - keep as is */ -}}
    {{- $_ := set $result "items" (append (get $result "items") $baseItem) -}}
  {{- end -}}
{{- end -}}

{{- /* Second pass: add new items from patch that weren't in base */ -}}
{{- range $patchItem := $patchArray -}}
  {{- if not (hasKey $processedNames $patchItem.name) -}}
    {{- $_ := set $result "items" (append (get $result "items") $patchItem) -}}
  {{- end -}}
{{- end -}}

{{ toYaml $result }}
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
