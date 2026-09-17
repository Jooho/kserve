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

package captureconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

func TestCaptureConfigRoundTrip(t *testing.T) {
	want := CaptureConfig{
		Version:     CurrentVersion,
		CacheDir:    "/workspace/cache/0",
		TargetImage: "registry.example/team/cache:session",
		Capture: CaptureIdentity{
			Name:      "capture",
			Namespace: "team",
			SessionID: "session-id",
		},
		CachePaths: []v1alpha1.KernelCachePath{{
			ContainerName: "kserve-container",
			ContainerPath: "/tmp/vllm",
			OCIPath:       "io.vllm.cache",
		}},
	}

	value, err := MarshalCaptureConfig(want)
	require.NoError(t, err)
	got, err := ParseCaptureConfig(value)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestParseCaptureConfigRejectsUnsupportedVersion(t *testing.T) {
	_, err := ParseCaptureConfig(`{"version":2}`)
	require.EqualError(t, err, "unsupported capture config version 2")
}

func TestReadinessConfigRoundTrip(t *testing.T) {
	want := ReadinessConfig{
		URL:                               "http://127.0.0.1:8080/health",
		MCVCaptureReadinessTimeoutSeconds: 600,
	}

	value, err := MarshalReadinessConfig(want)
	require.NoError(t, err)
	got, err := ParseReadinessConfig(value)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestRuntimeInfoRoundTrip(t *testing.T) {
	want := RuntimeInfo{
		CommandHash:  "command-hash",
		ArgsHash:     "args-hash",
		ModelURIHash: "model-hash",
	}

	value, err := MarshalRuntimeInfo(want)
	require.NoError(t, err)
	got, err := ParseRuntimeInfo(value)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
