{{/*
Expand the name of the chart.
*/}}
{{- define "llm-isvc-resources.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "llm-isvc-resources.fullname" -}}
{{- if contains .Chart.Name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "llm-isvc-resources.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "llm-isvc-resources.labels" -}}
helm.sh/chart: {{ include "llm-isvc-resources.chart" . }}
{{ include "llm-isvc-resources.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "llm-isvc-resources.selectorLabels" -}}
app.kubernetes.io/name: {{ include "llm-isvc-resources.deploymentName" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the deployment name
*/}}
{{- define "llm-isvc-resources.deploymentName" -}}
llmisvc-controller-manager
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "llm-isvc-resources.serviceAccountName" -}}
{{- default (include "llm-isvc-resources.deploymentName" .) .Values.kserve.llmisvc.controller.serviceAccount.name }}
{{- end }}

{{/*
Return the proper image name
*/}}
{{- define "llm-isvc-resources.image" -}}
{{- $repositoryName := .Values.kserve.llmisvc.controller.image -}}
{{- $tag := .Values.kserve.llmisvc.controller.tag | toString -}}
{{- printf "%s:%s" $repositoryName $tag -}}
{{- end }}

{{/*
Return the proper image pull policy
*/}}
{{- define "llm-isvc-resources.imagePullPolicy" -}}
{{- .Values.kserve.llmisvc.controller.imagePullPolicy | default "IfNotPresent" }}
{{- end }}

{{/*
Return the proper image pull secrets
*/}}
{{- define "llm-isvc-resources.imagePullSecrets" -}}
{{- if .Values.kserve.llmisvc.controller.imagePullSecrets }}
imagePullSecrets:
{{- range .Values.kserve.llmisvc.controller.imagePullSecrets }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Deep merge two dictionaries (recursive)

Usage: include "llm-isvc-resources.deepMerge" (list $base $patch) | fromYaml

Features:
- Merges dictionaries (maps) recursively
- Arrays with named elements (e.g., containers, env, volumeMounts) are merged by name
- Other arrays in $patch completely replace arrays in $base

Example:
  Base:    {a: {b: 1, c: 2}, containers: [{name: foo, x: 1}]}
  Patch:   {a: {b: 10}, containers: [{name: foo, y: 2}]}
  Result:  {a: {b: 10, c: 2}, containers: [{name: foo, x: 1, y: 2}]}
*/}}
{{- define "llm-isvc-resources.deepMerge" -}}
{{- $base := index . 0 -}}
{{- $patch := index . 1 -}}
{{- range $key, $value := $patch -}}
  {{- if hasKey $base $key -}}
    {{- $baseValue := get $base $key -}}
    {{- if and (kindIs "map" $value) (kindIs "map" $baseValue) -}}
      {{- /* Recursively merge nested maps */ -}}
      {{- $merged := include "llm-isvc-resources.deepMerge" (list $baseValue $value) | fromYaml -}}
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
        {{- $mergedResult := include "llm-isvc-resources.mergeArrayByName" (list $baseValue $value) | fromYaml -}}
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

Usage: include "llm-isvc-resources.mergeArrayByName" (list $baseArray $patchArray) | fromYaml

For each item in patch array:
- If matching name exists in base, merge them (recursively)
- Otherwise add as new item
Items in base without matching patch are preserved
*/}}
{{- define "llm-isvc-resources.mergeArrayByName" -}}
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
      {{- $merged := include "llm-isvc-resources.deepMerge" (list $baseItem $patchItem) | fromYaml -}}
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
{{- define "llm-isvc-resources.replaceNamespace" -}}
{{- $content := index . 0 -}}
{{- $namespace := index . 1 -}}
{{- $pattern := "namespace: kserve\n" -}}
{{- $replacement := printf "namespace: %s\n" $namespace -}}
{{- $content | replace $pattern $replacement -}}
{{- end -}}
