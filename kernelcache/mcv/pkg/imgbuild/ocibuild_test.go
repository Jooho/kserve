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

package imgbuild

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/require"

	cachesnapshot "github.com/kserve/kserve/kernelcache/mcv/pkg/snapshot"
)

func TestOCISnapshotRejectsUnsafeEntries(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "whiteout"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			switch kind {
			case "symlink":
				secret := filepath.Join(t.TempDir(), "secret")
				require.NoError(t, os.WriteFile(secret, []byte("private"), 0o600))
				require.NoError(t, os.Symlink(secret, filepath.Join(source, "cache")))
			case "fifo":
				require.NoError(t, syscall.Mkfifo(filepath.Join(source, "pipe"), 0o600))
			case "whiteout":
				require.NoError(t, os.WriteFile(filepath.Join(source, ".wh.cache"), nil, 0o600))
			}
			require.Error(t, snapshotOCICache(context.Background(), source, filepath.Join(t.TempDir(), "snapshot")))
		})
	}
}

func TestOCISnapshotAllowsOnlyMountedCacheLinks(t *testing.T) {
	allowed := t.TempDir()
	t.Setenv("MCV_CACHE_LINK_ROOT", allowed)
	cacheFile := filepath.Join(allowed, "kernel.bin")
	require.NoError(t, os.WriteFile(cacheFile, []byte("cached"), 0o600))
	source := t.TempDir()
	require.NoError(t, os.Symlink(cacheFile, filepath.Join(source, "kernel.bin")))
	destination := filepath.Join(t.TempDir(), "snapshot")
	require.NoError(t, snapshotOCICache(context.Background(), source, destination))
	data, err := os.ReadFile(filepath.Join(destination, "kernel.bin")) // #nosec G304 -- destination is a test temporary directory
	require.NoError(t, err)
	require.Equal(t, "cached", string(data))
	outside := filepath.Join(t.TempDir(), "credential")
	require.NoError(t, os.WriteFile(outside, []byte("private"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "escape")))
	require.Error(t, snapshotOCICache(context.Background(), source, filepath.Join(t.TempDir(), "snapshot")))
}

func TestOCISnapshotIncludesOnlySelectedDirectoryTrees(t *testing.T) {
	source := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(source, "torch_compile_cache", "existing"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(source, "torch_compile_cache", "new-hash", "nested"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(source, "unrelated"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "torch_compile_cache", "existing", "old.bin"), []byte("old"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "torch_compile_cache", "new-hash", "kernel.bin"), []byte("new"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "unrelated", "other.bin"), []byte("other"), 0o600))

	destination := filepath.Join(t.TempDir(), "snapshot")
	require.NoError(t, snapshotOCICacheDirectories(
		context.Background(), source, destination, []string{"torch_compile_cache/new-hash"}, nil,
	))

	data, err := os.ReadFile(filepath.Join(destination, "torch_compile_cache", "new-hash", "kernel.bin")) // #nosec G304 -- destination is a test temporary directory
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	require.DirExists(t, filepath.Join(destination, "torch_compile_cache", "new-hash", "nested"))
	require.NoFileExists(t, filepath.Join(destination, "torch_compile_cache", "existing", "old.bin"))
	require.NoFileExists(t, filepath.Join(destination, "unrelated", "other.bin"))
}

func TestOCISnapshotExcludesDirectoryTrees(t *testing.T) {
	source := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(source, "included"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "included", "kernel.bin"), []byte("included"), 0o600))

	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("private"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(source, "dummy_cache"), 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "dummy_cache", "escape")))

	destination := filepath.Join(t.TempDir(), "snapshot")
	require.NoError(t, snapshotOCICacheDirectories(
		context.Background(), source, destination, nil, []string{"dummy_cache"},
	))
	require.FileExists(t, filepath.Join(destination, "included", "kernel.bin"))
	require.NoDirExists(t, filepath.Join(destination, "dummy_cache"))
}

func TestCreateDeltaImageSkipsUnchangedCache(t *testing.T) {
	cacheDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, "torch_compile_cache", "existing"), 0o700))
	document, err := cachesnapshot.Capture([]string{cacheDir})
	require.NoError(t, err)
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	require.NoError(t, cachesnapshot.Write(snapshotPath, document))

	result, err := (&ociBuilder{}).CreateDeltaImageWithResult("example.com/cache:test", cacheDir, snapshotPath)
	require.NoError(t, err)
	require.Equal(t, CreateStateUnchanged, result.State)
	require.Empty(t, result.ImageReference)
}

func TestCreateDeltaImageSkipsOnlyExcludedDirectories(t *testing.T) {
	cacheDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, "torch_compile_cache", "existing"), 0o700))
	document, err := cachesnapshot.CaptureRoots([]cachesnapshot.RootOptions{{
		Source:              cacheDir,
		ExcludedDirectories: []string{"dummy_cache"},
	}})
	require.NoError(t, err)
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	require.NoError(t, cachesnapshot.Write(snapshotPath, document))
	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, "dummy_cache", "nested"), 0o700))

	result, err := (&ociBuilder{}).CreateDeltaImageWithResult("example.com/cache:test", cacheDir, snapshotPath)
	require.NoError(t, err)
	require.Equal(t, CreateStateUnchanged, result.State)
	require.Empty(t, result.ImageReference)
}

func TestCreateDeltaImageSkipsInitialExcludedOnlyCache(t *testing.T) {
	cacheDir := t.TempDir()
	document, err := cachesnapshot.CaptureRoots([]cachesnapshot.RootOptions{{
		Source:              cacheDir,
		ExcludedDirectories: []string{"dummy_cache"},
	}})
	require.NoError(t, err)
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	require.NoError(t, cachesnapshot.Write(snapshotPath, document))
	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, "dummy_cache", "nested"), 0o700))

	result, err := (&ociBuilder{}).CreateDeltaImageWithResult("example.com/cache:test", cacheDir, snapshotPath)
	require.NoError(t, err)
	require.Equal(t, CreateStateUnchanged, result.State)
	require.Empty(t, result.ImageReference)
}

func TestCreateDeltaImageRequiresSnapshot(t *testing.T) {
	_, err := (&ociBuilder{}).CreateDeltaImageWithResult(
		"example.com/cache:test", t.TempDir(), filepath.Join(t.TempDir(), "missing.json"),
	)
	require.ErrorContains(t, err, "read delta snapshot")
}

func TestSnapshotHasDirectories(t *testing.T) {
	tests := []struct {
		name     string
		document *cachesnapshot.Document
		expected bool
	}{
		{
			name: "empty snapshot is an initial capture",
			document: &cachesnapshot.Document{
				Version: cachesnapshot.Version,
				Roots:   []cachesnapshot.Root{{Source: "/tmp/cache"}},
			},
			expected: false,
		},
		{
			name: "snapshot with directories is eligible for delta capture",
			document: &cachesnapshot.Document{
				Version: cachesnapshot.Version,
				Roots: []cachesnapshot.Root{{
					Source:      "/tmp/cache",
					Directories: []string{"torch_compile_cache"},
				}},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, snapshotHasDirectories(tt.document))
		})
	}
}

func TestOCIPackageRegistryRoundTrip(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(source, "kernel.bin"), []byte("compiled-cache"), 0o600))
	workDir := t.TempDir()
	cacheDir := filepath.Join(workDir, "cache")
	require.NoError(t, snapshotOCICache(ctx, source, cacheDir))
	manifestDir := filepath.Join(workDir, "manifest")
	require.NoError(t, os.Mkdir(manifestDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(manifestDir, "manifest.json"), []byte(`{"vllm":[]}`), 0o600))
	prep := &buildContext{
		CacheBuildDir: cacheDir, CacheTag: "io.vllm.cache",
		ManifestBuildDir: manifestDir, ManifestTag: "io.vllm.manifest",
		Labels: map[string]string{"cache.vllm.image/format": "test"},
	}
	img, err := packageOCIImage(ctx, prep, filepath.Join(workDir, "layer.tar"))
	require.NoError(t, err)
	digest, err := img.Digest()
	require.NoError(t, err)

	server := httptest.NewServer(registry.New())
	defer server.Close()
	ref, err := name.NewTag(strings.TrimPrefix(server.URL, "http://")+"/cache:test", name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, img, remote.WithContext(ctx)))
	actual, err := remote.Image(ref, remote.WithContext(ctx))
	require.NoError(t, err)
	actualDigest, err := actual.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, actualDigest)
	manifest, err := actual.Manifest()
	require.NoError(t, err)
	require.Equal(t, types.OCIManifestSchema1, manifest.MediaType)
	require.Equal(t, types.OCIConfigJSON, manifest.Config.MediaType)
	cfg, err := actual.ConfigFile()
	require.NoError(t, err)
	require.Equal(t, prep.Labels, cfg.Config.Labels)
	layers, err := actual.Layers()
	require.NoError(t, err)
	require.Len(t, layers, 1)
	require.Equal(t, types.OCILayer, manifest.Layers[0].MediaType)
	reader, err := layers[0].Uncompressed()
	require.NoError(t, err)
	defer reader.Close()
	tr := tar.NewReader(reader)
	files := map[string]string{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		require.Zero(t, header.Uid)
		require.Zero(t, header.Gid)
		if header.Typeflag == tar.TypeDir {
			require.EqualValues(t, 0o755, header.Mode)
			continue
		}
		require.EqualValues(t, 0o644, header.Mode)
		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[header.Name] = string(content)
	}
	require.Equal(t, map[string]string{
		"io.vllm.cache/kernel.bin":       "compiled-cache",
		"io.vllm.manifest/manifest.json": `{"vllm":[]}`,
	}, files)
}
