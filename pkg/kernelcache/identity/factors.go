/*
Copyright 2026 The KServe Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package identity

import (
	"regexp"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

type factorValueType int

const (
	factorValueString factorValueType = iota
	factorValueInteger
	factorValueBoolean
	factorValueCommaSeparated
	factorValueHash
)

// FactorDefinition describes one runtime setting that can affect a cache artifact.
// Keep option and environment definitions here so extraction and selection use the
// same vocabulary.
type FactorDefinition struct {
	Key             string
	OptionAliases   []string
	EnvironmentName string
	ValueType       factorValueType
	Compatibility   bool
	Scoring         bool
}

const (
	PipelineParallelSizeFactor = "pipelineParallelSize"
	MaxModelLenFactor          = "option.maxModelLen"
	DTypeFactor                = "option.dtype"
	QuantizationFactor         = "option.quantization"
	QuantizationConfigFactor   = "option.quantizationConfigHash"
	KVCacheDTypeFactor         = "option.kvCacheDtype"
	BlockSizeFactor            = "option.blockSize"
	MaxNumBatchedTokensFactor  = "option.maxNumBatchedTokens"
	MaxNumSeqsFactor           = "option.maxNumSeqs"
	ChunkedPrefillFactor       = "option.enableChunkedPrefill"
	DisableSlidingWindowFactor = "option.disableSlidingWindow"
	EnforceEagerFactor         = "option.enforceEager"
	AttentionBackendFactor     = "option.attentionBackend"
	CompilationConfigFactor    = "option.compilationConfigHash"
)

var runtimeFactorDefinitions = []FactorDefinition{
	{Key: TensorParallelSizeFactor, OptionAliases: []string{"--tensor-parallel-size", "--tensor_parallel_size", "-tp", "--tp"}, ValueType: factorValueInteger, Compatibility: true, Scoring: true},
	{Key: PipelineParallelSizeFactor, OptionAliases: []string{"--pipeline-parallel-size", "--pipeline_parallel_size", "-pp", "--pp"}, ValueType: factorValueInteger, Compatibility: true, Scoring: true},
	{Key: MaxModelLenFactor, OptionAliases: []string{"--max-model-len", "--max_model_len"}, ValueType: factorValueInteger, Compatibility: true, Scoring: true},
	{Key: DTypeFactor, OptionAliases: []string{"--dtype"}, ValueType: factorValueString, Compatibility: true, Scoring: true},
	{Key: QuantizationFactor, OptionAliases: []string{"--quantization", "-q"}, ValueType: factorValueString, Compatibility: true, Scoring: true},
	{Key: QuantizationConfigFactor, OptionAliases: []string{"--quantization-config", "--quantization_config"}, ValueType: factorValueHash, Compatibility: true, Scoring: true},
	{Key: KVCacheDTypeFactor, OptionAliases: []string{"--kv-cache-dtype", "--kv_cache_dtype"}, ValueType: factorValueString, Compatibility: true, Scoring: true},
	{Key: BlockSizeFactor, OptionAliases: []string{"--block-size", "--block_size"}, ValueType: factorValueInteger, Compatibility: true, Scoring: true},
	{Key: MaxNumBatchedTokensFactor, OptionAliases: []string{"--max-num-batched-tokens", "--max_num_batched_tokens"}, ValueType: factorValueInteger, Compatibility: true, Scoring: true},
	{Key: MaxNumSeqsFactor, OptionAliases: []string{"--max-num-seqs", "--max_num_seqs"}, ValueType: factorValueInteger, Compatibility: true, Scoring: true},
	{Key: ChunkedPrefillFactor, OptionAliases: []string{"--enable-chunked-prefill", "--enable_chunked_prefill"}, ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: DisableSlidingWindowFactor, OptionAliases: []string{"--disable-sliding-window", "--disable_sliding_window"}, ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: EnforceEagerFactor, OptionAliases: []string{"--enforce-eager", "--enforce_eager"}, ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: AttentionBackendFactor, OptionAliases: []string{"--attention-backend", "--attention_backend"}, ValueType: factorValueString, Compatibility: true, Scoring: true},
	{Key: CompilationConfigFactor, OptionAliases: []string{"--compilation-config", "--compilation_config", "-cc"}, ValueType: factorValueHash, Compatibility: true, Scoring: true},

	{Key: "env.VLLM_USE_AOT_COMPILE", EnvironmentName: "VLLM_USE_AOT_COMPILE", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_USE_MEGA_AOT_ARTIFACT", EnvironmentName: "VLLM_USE_MEGA_AOT_ARTIFACT", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_USE_STANDALONE_COMPILE", EnvironmentName: "VLLM_USE_STANDALONE_COMPILE", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_ENABLE_PREGRAD_PASSES", EnvironmentName: "VLLM_ENABLE_PREGRAD_PASSES", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_ENABLE_INDUCTOR_MAX_AUTOTUNE", EnvironmentName: "VLLM_ENABLE_INDUCTOR_MAX_AUTOTUNE", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_ENABLE_INDUCTOR_COORDINATE_DESCENT_TUNING", EnvironmentName: "VLLM_ENABLE_INDUCTOR_COORDINATE_DESCENT_TUNING", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_FLOAT32_MATMUL_PRECISION", EnvironmentName: "VLLM_FLOAT32_MATMUL_PRECISION", ValueType: factorValueString, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_BATCH_INVARIANT", EnvironmentName: "VLLM_BATCH_INVARIANT", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_TRITON_USE_TD", EnvironmentName: "VLLM_TRITON_USE_TD", ValueType: factorValueBoolean, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_DISABLED_KERNELS", EnvironmentName: "VLLM_DISABLED_KERNELS", ValueType: factorValueCommaSeparated, Compatibility: true, Scoring: true},
	{Key: "env.VLLM_PP_LAYER_PARTITION", EnvironmentName: "VLLM_PP_LAYER_PARTITION", ValueType: factorValueString, Compatibility: true, Scoring: true},
}

var argumentTokenPattern = regexp.MustCompile(`(?:[^\s"']+|"[^"]*"|'[^']*')+`)

// RuntimeFactorDefinitions returns a copy of the supported factor catalog.
func RuntimeFactorDefinitions() []FactorDefinition {
	return append([]FactorDefinition(nil), runtimeFactorDefinitions...)
}

func IsCompatibilityFactor(key string) bool {
	for _, definition := range runtimeFactorDefinitions {
		if definition.Key == key {
			return definition.Compatibility
		}
	}
	return key == RuntimeImageFactor || key == ModelURIHashFactor
}

func IsScoringFactor(key string) bool {
	for _, definition := range runtimeFactorDefinitions {
		if definition.Key == key {
			return definition.Scoring
		}
	}
	return key == RuntimeImageFactor || key == ModelURIHashFactor
}

// ScoringFactorKeys returns the complete set of factors considered by weighted
// compatibility matching. A factor absent from both identities is not scored.
func ScoringFactorKeys() []string {
	keys := []string{RuntimeImageFactor, ModelURIHashFactor}
	for _, definition := range runtimeFactorDefinitions {
		if definition.Scoring {
			keys = append(keys, definition.Key)
		}
	}
	return keys
}

func isDefinedRuntimeFactor(key string) bool {
	for _, definition := range runtimeFactorDefinitions {
		if definition.Key == key {
			return true
		}
	}
	return false
}

// IsStableRuntimeImage reports whether the image reference can safely select a
// previously built cache. Digest references and non-latest tags are stable.
func IsStableRuntimeImage(image string) bool {
	imageType, err := classifyImageReference(image)
	return err == nil && imageType != imageReferenceFloating
}

// ExtractRuntimeFactors reads only known vLLM options and allowlisted environment
// variables. It intentionally does not interpret shell syntax or expand values.
func ExtractRuntimeFactors(command, args []string, environment map[string]string) map[string]string {
	factors := make(map[string]string)
	values := argumentTokenPattern.FindAllString(strings.Join(append(append([]string(nil), command...), args...), " "), -1)
	for index := 0; index < len(values); index++ {
		argument := values[index]
		for _, definition := range runtimeFactorDefinitions {
			if len(definition.OptionAliases) == 0 {
				continue
			}
			value, consumed, found := optionValue(definition, argument, valueAt(values, index+1))
			if !found {
				continue
			}
			if normalized, ok := normalizeFactorValue(definition.ValueType, value); ok {
				factors[definition.Key] = normalized
			}
			if consumed {
				index++
			}
			break
		}
	}
	for _, definition := range runtimeFactorDefinitions {
		if definition.EnvironmentName == "" {
			continue
		}
		value, exists := environment[definition.EnvironmentName]
		if !exists {
			continue
		}
		if normalized, ok := normalizeFactorValue(definition.ValueType, value); ok {
			factors[definition.Key] = normalized
		}
	}
	return factors
}

// ExtractContainerRuntimeFactors reads command, args, and literal environment
// values from a runtime container. ValueFrom and envFrom are intentionally not
// resolved because their values are not available during admission.
func ExtractContainerRuntimeFactors(container *corev1.Container) map[string]string {
	if container == nil {
		return map[string]string{}
	}
	environment := make(map[string]string, len(container.Env))
	for _, variable := range container.Env {
		if variable.ValueFrom == nil {
			environment[variable.Name] = variable.Value
		}
	}
	return ExtractRuntimeFactors(container.Command, container.Args, environment)
}

func optionValue(definition FactorDefinition, argument, next string) (string, bool, bool) {
	for _, alias := range definition.OptionAliases {
		if definition.ValueType == factorValueBoolean {
			if argument == "--no-"+strings.TrimPrefix(alias, "--") {
				return "false", false, true
			}
			if argument == alias {
				if normalized, ok := normalizeFactorValue(factorValueBoolean, next); ok {
					return normalized, true, true
				}
				return "true", false, true
			}
		}
		if argument == alias {
			if next == "" || strings.HasPrefix(next, "-") {
				return "", false, true
			}
			return next, true, true
		}
		if strings.HasPrefix(argument, alias+"=") {
			return strings.TrimPrefix(argument, alias+"="), false, true
		}
	}
	return "", false, false
}

func valueAt(values []string, index int) string {
	if index >= len(values) {
		return ""
	}
	return values[index]
}

func normalizeFactorValue(valueType factorValueType, value string) (string, bool) {
	value = strings.Trim(strings.TrimSpace(value), "\"'")
	if value == "" {
		return "", false
	}
	switch valueType {
	case factorValueInteger:
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return "", false
		}
		return strconv.Itoa(parsed), true
	case factorValueBoolean:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			if value == "1" {
				return "true", true
			}
			if value == "0" {
				return "false", true
			}
			return "", false
		}
		return strconv.FormatBool(parsed), true
	case factorValueCommaSeparated:
		parts := strings.Split(value, ",")
		for index := range parts {
			parts[index] = strings.TrimSpace(parts[index])
		}
		return strings.Join(parts, ","), true
	case factorValueHash:
		return HashStrings([]string{value}), true
	default:
		return strings.ToLower(value), true
	}
}
