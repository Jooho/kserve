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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

const (
	CurrentVersion = 1

	CaptureConfigEnv   = "MCV_CAPTURE_CONFIG"
	ReadinessConfigEnv = "MCV_READINESS_CONFIG"
	RuntimeInfoEnv     = "MCV_RUNTIME_INFO"
)

// CaptureConfig contains the values shared by the webhook, controller, and
// capture entrypoint for one capture session.
type CaptureConfig struct {
	Version     int                        `json:"version"`
	CacheDir    string                     `json:"cacheDir,omitempty"`
	TargetImage string                     `json:"targetImage,omitempty"`
	Capture     CaptureIdentity            `json:"capture"`
	CachePaths  []v1alpha1.KernelCachePath `json:"cachePaths,omitempty"`
}

type CaptureIdentity struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	SessionID string `json:"sessionID"`
}

type ReadinessConfig struct {
	URL                               string `json:"url"`
	MCVCaptureReadinessTimeoutSeconds int64  `json:"mcvCaptureReadinessTimeoutSeconds"`
}

type RuntimeInfo struct {
	CommandHash  string `json:"commandHash,omitempty"`
	ArgsHash     string `json:"argsHash,omitempty"`
	ModelURIHash string `json:"modelURIHash,omitempty"`
}

func MarshalCaptureConfig(config CaptureConfig) (string, error) {
	if config.Version == 0 {
		config.Version = CurrentVersion
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal capture config: %w", err)
	}
	return string(data), nil
}

func ParseCaptureConfig(value string) (CaptureConfig, error) {
	var config CaptureConfig
	if err := decode(value, &config); err != nil {
		return config, fmt.Errorf("parse capture config: %w", err)
	}
	if config.Version != CurrentVersion {
		return config, fmt.Errorf("unsupported capture config version %d", config.Version)
	}
	return config, nil
}

func MarshalReadinessConfig(config ReadinessConfig) (string, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal readiness config: %w", err)
	}
	return string(data), nil
}

func ParseReadinessConfig(value string) (ReadinessConfig, error) {
	var config ReadinessConfig
	if err := decode(value, &config); err != nil {
		return config, fmt.Errorf("parse readiness config: %w", err)
	}
	return config, nil
}

func MarshalRuntimeInfo(info RuntimeInfo) (string, error) {
	data, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("marshal runtime info: %w", err)
	}
	return string(data), nil
}

func ParseRuntimeInfo(value string) (RuntimeInfo, error) {
	var info RuntimeInfo
	if err := decode(value, &info); err != nil {
		return info, fmt.Errorf("parse runtime info: %w", err)
	}
	return info, nil
}

func decode(value string, target any) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value is empty")
	}
	if err := json.Unmarshal([]byte(value), target); err != nil {
		return err
	}
	return nil
}
