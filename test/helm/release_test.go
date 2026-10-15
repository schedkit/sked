package helm

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func packageChart(t *testing.T, tag string) string {
	t.Helper()
	helm(t)
	destination := t.TempDir()
	command(t, "bash", "hack/package-helm.sh", tag, destination)
	return filepath.Join(destination, "sked-"+strings.TrimPrefix(tag, "v")+".tgz")
}

func TestReleaseChartVersions(t *testing.T) {
	helm(t)
	sourcePath := filepath.Join(root(t), "charts/sked/Chart.yaml")
	before, err := os.ReadFile(sourcePath)
	require.NoError(t, err)

	for _, tag := range []string{"v1.2.3", "2.0.0", "v3.4.5-rc.1", "4.0.0-0.dev"} {
		t.Run(tag, func(t *testing.T) {
			archive := packageChart(t, tag)
			var chart struct {
				Version    string `json:"version"`
				AppVersion string `json:"appVersion"`
			}
			require.NoError(t, yaml.Unmarshal(command(t, helm(t), "show", "chart", archive), &chart))
			require.Equal(t, strings.TrimPrefix(tag, "v"), chart.Version)
			require.Equal(t, tag, chart.AppVersion)
			docs := decode(t, command(t, helm(t), "template", "sked", archive, "--include-crds"))
			require.Equal(t, "ghcr.io/schedkit/sked:"+tag, manager(t, docs, "sked")["image"])
			require.Contains(t, docs, "CustomResourceDefinition/schedulers.sked.schedkit.io")
		})
	}

	after, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestReleaseChartRejectsInvalidTags(t *testing.T) {
	helm(t)
	for _, tag := range []string{
		"latest", "v1.2", "v01.2.3", "v1.2.3-01", "v1.2.3-rc..1",
		"v1.2.3+build", "release/1.2.3", "v1.2.3\ninjected", "-1.2.3",
		"v1.2.3-" + strings.Repeat("a", 128),
	} {
		t.Run(tag, func(t *testing.T) {
			destination := t.TempDir()
			cmd := exec.Command("bash", "hack/package-helm.sh", tag, destination)
			cmd.Dir = root(t)
			out, err := cmd.CombinedOutput()
			require.Error(t, err, "%s", out)
			require.Contains(t, string(out), "Expected a release tag")
			files, err := os.ReadDir(destination)
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestPublishHelmRepository(t *testing.T) {
	helm(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	command(t, "git", "init", "--bare", "--initial-branch=main", remote)
	stable := packageChart(t, "v2.0.0")
	older := packageChart(t, "v1.2.3")
	prerelease := packageChart(t, "v3.0.0-rc.1")
	command(t, "bash", "hack/publish-helm-repo.sh", stable, remote)

	t.Run("concurrent releases", func(t *testing.T) {
		for _, archive := range []string{older, prerelease} {
			t.Run(filepath.Base(archive), func(t *testing.T) {
				t.Parallel()
				command(t, "bash", "hack/publish-helm-repo.sh", archive, remote)
			})
		}
	})
	command(t, "bash", "hack/publish-helm-repo.sh", stable, remote)

	site := filepath.Join(t.TempDir(), "site")
	command(t, "git", "clone", "--branch=gh-pages", remote, site)
	require.FileExists(t, filepath.Join(site, ".nojekyll"))
	require.NoFileExists(t, filepath.Join(site, "Chart.yaml"))
	data, err := os.ReadFile(filepath.Join(site, "index.yaml"))
	require.NoError(t, err)
	var index struct {
		Entries map[string][]struct {
			Version string   `json:"version"`
			Digest  string   `json:"digest"`
			URLs    []string `json:"urls"`
		} `json:"entries"`
	}
	require.NoError(t, yaml.Unmarshal(data, &index))
	require.Len(t, index.Entries["sked"], 3)
	for _, entry := range index.Entries["sked"] {
		archive := "sked-" + entry.Version + ".tgz"
		require.Equal(t, []string{archive}, entry.URLs)
		data, err := os.ReadFile(filepath.Join(site, archive))
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), entry.Digest)
	}

	server := httptest.NewServer(http.FileServer(http.Dir(site)))
	defer server.Close()
	client := t.TempDir()
	config := filepath.Join(client, "repositories.yaml")
	cache := filepath.Join(client, "cache")
	command(t, helm(t), "repo", "add", "release-test", server.URL,
		"--repository-config", config, "--repository-cache", cache)
	command(t, helm(t), "pull", "release-test/sked", "--destination", client,
		"--repository-config", config, "--repository-cache", cache)
	want, err := os.ReadFile(stable)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(client, filepath.Base(stable)))
	require.NoError(t, err)
	require.Equal(t, want, got)
	command(t, helm(t), "pull", "release-test/sked", "--version", "1.2.3", "--destination", client,
		"--repository-config", config, "--repository-cache", cache)
	require.FileExists(t, filepath.Join(client, filepath.Base(older)))
	command(t, helm(t), "pull", "release-test/sked", "--version", "3.0.0-rc.1", "--destination", client,
		"--repository-config", config, "--repository-cache", cache)
	require.FileExists(t, filepath.Join(client, filepath.Base(prerelease)))

	head := command(t, "git", "--git-dir="+remote, "rev-parse", "gh-pages")
	require.NoError(t, os.WriteFile(stable, append(want, '\n'), 0o600))
	cmd := exec.Command("bash", "hack/publish-helm-repo.sh", stable, remote)
	cmd.Dir = root(t)
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "%s", out)
	require.Contains(t, string(out), "Refusing to overwrite an existing chart version")
	require.Equal(t, head, command(t, "git", "--git-dir="+remote, "rev-parse", "gh-pages"))
}
