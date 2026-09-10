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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRuntimeConfigFactors(t *testing.T) {
	factors := ParseRuntimeConfigFactors(
		[]string{"python", "-m", "vllm.entrypoints.openai.api_server", "--dtype=bfloat16", "-tp", "2"},
		[]string{
			"--max-model-len", "4096",
			"--quantization", "awq",
			"--compilation-config", "{\"level\":3}",
			"--enable-chunked-prefill",
			"--no-enable-chunked-prefill",
		},
		map[string]string{
			"VLLM_USE_AOT_COMPILE":              "1",
			"VLLM_ENABLE_INDUCTOR_MAX_AUTOTUNE": "true",
			"VLLM_DISABLED_KERNELS":             "Marlin, ExLlama",
			"VLLM_CACHE_ROOT":                   "/tmp/vllm",
		},
	)

	require.Equal(t, map[string]string{
		"option.dtype":                          "bfloat16",
		"tensorParallelSize":                    "2",
		"option.maxModelLen":                    "4096",
		"option.quantization":                   "awq",
		"option.compilationConfigHash":          HashStrings([]string{"{\"level\":3}"}),
		"option.enableChunkedPrefill":           "false",
		"env.VLLM_USE_AOT_COMPILE":              "true",
		"env.VLLM_ENABLE_INDUCTOR_MAX_AUTOTUNE": "true",
		"env.VLLM_DISABLED_KERNELS":             "Marlin,ExLlama",
	}, factors)
}

func TestParseRuntimeConfigFactorsUsesLastOptionValue(t *testing.T) {
	factors := ParseRuntimeConfigFactors(
		[]string{"--max-num-seqs=128", "--tensor_parallel_size=2"},
		[]string{"--max-num-seqs", "256", "--tp=4"},
		nil,
	)

	require.Equal(t, "256", factors["option.maxNumSeqs"])
	require.Equal(t, "4", factors[TensorParallelSizeFactor])
}

func TestParseRuntimeConfigFactorsIgnoresUnsupportedAndInvalidValues(t *testing.T) {
	factors := ParseRuntimeConfigFactors(
		[]string{"--unknown-option=value", "--tensor-parallel-size=0"},
		nil,
		map[string]string{
			"VLLM_USE_AOT_COMPILE": "not-a-bool",
			"VLLM_CACHE_ROOT":      "/tmp/vllm",
		},
	)

	require.Empty(t, factors)
}
