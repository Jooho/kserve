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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

const (
	NamespaceFactor          = "namespace"
	WorkloadNameFactor       = "workloadName"
	RuntimeImageFactor       = "runtimeImage"
	ModelURIHashFactor       = "modelURIHash"
	TensorParallelSizeFactor = "tensorParallelSize"
	CommandHashFactor        = "commandHash"
	ArgsHashFactor           = "argsHash"
)

type Input struct {
	Namespace          string
	WorkloadName       string
	RuntimeImage       string
	ModelURIHash       string
	TensorParallelSize string
	CommandHash        string
	ArgsHash           string
	RuntimeFactors     map[string]string
}

type imageReferenceType int

const (
	imageReferenceFloating imageReferenceType = iota
	imageReferenceTag
	imageReferenceDigest
)

func Build(input Input) (v1alpha1.KernelCacheIdentity, error) {
	if input.Namespace == "" || input.WorkloadName == "" {
		return v1alpha1.KernelCacheIdentity{}, errors.New("namespace and workload name are required")
	}
	if !IsSHA256(input.ModelURIHash) {
		return v1alpha1.KernelCacheIdentity{}, errors.New("model URI hash is required and must be a SHA-256 value")
	}
	imageType, err := classifyImageReference(input.RuntimeImage)
	if err != nil {
		return v1alpha1.KernelCacheIdentity{}, err
	}

	factors := map[string]string{
		NamespaceFactor:    input.Namespace,
		WorkloadNameFactor: input.WorkloadName,
		RuntimeImageFactor: input.RuntimeImage,
		ModelURIHashFactor: input.ModelURIHash,
	}
	addOptionalFactor(factors, TensorParallelSizeFactor, input.TensorParallelSize)
	addOptionalFactor(factors, CommandHashFactor, input.CommandHash)
	addOptionalFactor(factors, ArgsHashFactor, input.ArgsHash)
	addRuntimeFactors(factors, input.RuntimeFactors)

	identity := v1alpha1.KernelCacheIdentity{Factors: factors}
	if imageType == imageReferenceFloating {
		return identity, nil
	}

	compatibility := map[string]string{
		RuntimeImageFactor: input.RuntimeImage,
		ModelURIHashFactor: input.ModelURIHash,
	}
	addOptionalFactor(compatibility, TensorParallelSizeFactor, input.TensorParallelSize)
	addRuntimeFactors(compatibility, input.RuntimeFactors)
	identity.Footprints.CompatibilityFootprint = Calculate(compatibility)

	if imageType == imageReferenceDigest {
		workload := map[string]string{
			NamespaceFactor:    input.Namespace,
			WorkloadNameFactor: input.WorkloadName,
			RuntimeImageFactor: input.RuntimeImage,
			ModelURIHashFactor: input.ModelURIHash,
		}
		addOptionalFactor(workload, TensorParallelSizeFactor, input.TensorParallelSize)
		addOptionalFactor(workload, CommandHashFactor, input.CommandHash)
		addOptionalFactor(workload, ArgsHashFactor, input.ArgsHash)
		identity.Footprints.WorkloadFootprint = Calculate(workload)
	}
	return identity, nil
}

func ModelURIHash(raw string) string {
	if raw == "" {
		return ""
	}
	uri, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	uri.User = nil
	uri.RawQuery = ""
	uri.ForceQuery = false
	uri.Fragment = ""
	return HashStrings([]string{uri.String()})
}

func HashStrings(values []string) string {
	encoded, _ := json.Marshal(values)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum)
}

func Calculate(factors map[string]string) string {
	encoded, _ := json.Marshal(factors)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum)
}

func IsSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func addOptionalFactor(factors map[string]string, key, value string) {
	if value != "" {
		factors[key] = value
	}
}

func addRuntimeFactors(factors, runtimeFactors map[string]string) {
	for key, value := range runtimeFactors {
		if !isDefinedRuntimeFactor(key) || value == "" {
			continue
		}
		factors[key] = value
	}
}

func classifyImageReference(image string) (imageReferenceType, error) {
	image = strings.TrimSpace(image)
	if image == "" || strings.ContainsAny(image, " \t\n") {
		return imageReferenceFloating, errors.New("runtime image is required")
	}
	if at := strings.LastIndex(image, "@sha256:"); at >= 0 {
		if at == 0 || !IsSHA256(image[at+1:]) {
			return imageReferenceFloating, errors.New("runtime image digest is invalid")
		}
		return imageReferenceDigest, nil
	}
	if strings.Contains(image, "@") {
		return imageReferenceFloating, errors.New("runtime image digest is unsupported")
	}

	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon <= lastSlash {
		return imageReferenceFloating, nil
	}
	tag := image[lastColon+1:]
	if tag == "" {
		return imageReferenceFloating, errors.New("runtime image tag is empty")
	}
	if tag == "latest" {
		return imageReferenceFloating, nil
	}
	return imageReferenceTag, nil
}
