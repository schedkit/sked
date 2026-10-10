/*
Copyright 2025.

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

package controller

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type rbacManifest struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Rules []rbacRule `json:"rules"`
}

type rbacRule struct {
	APIGroups       []string `json:"apiGroups"`
	Resources       []string `json:"resources"`
	Verbs           []string `json:"verbs"`
	NonResourceURLs []string `json:"nonResourceURLs"`
}

func readRBACManifests(t *testing.T) []rbacManifest {
	t.Helper()

	roleFile := filepath.Join(projectRoot(t), "config", "rbac", "role.yaml")
	data, err := os.ReadFile(roleFile)
	require.NoError(t, err)

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var manifests []rbacManifest
	for {
		var manifest rbacManifest
		err := decoder.Decode(&manifest)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if manifest.Kind == "" {
			continue
		}
		manifests = append(manifests, manifest)
	}
	require.NotEmpty(t, manifests)
	return manifests
}

func findRBACManifest(t *testing.T, manifests []rbacManifest, kind, name string) rbacManifest {
	t.Helper()

	for _, manifest := range manifests {
		if manifest.Kind == kind && manifest.Metadata.Name == name {
			return manifest
		}
	}
	t.Fatalf("generated RBAC does not define %s/%s; re-run `make manifests` after changing the RBAC markers", kind, name)
	return rbacManifest{}
}

func requireRBACRule(t *testing.T, manifest rbacManifest, apiGroup, resource string, verbs []string) {
	t.Helper()

	for _, rule := range manifest.Rules {
		if !slices.Contains(rule.APIGroups, apiGroup) || !slices.Contains(rule.Resources, resource) {
			continue
		}
		for _, verb := range verbs {
			require.Contains(t, rule.Verbs, verb, "%s/%s is missing verb %q", manifest.Metadata.Name, resource, verb)
		}
		return
	}
	t.Fatalf("%s does not grant access to %s/%s; re-run `make manifests`", manifest.Metadata.Name, apiGroup, resource)
}

func TestGeneratedRBACManifests(t *testing.T) {
	manifests := readRBACManifests(t)

	manager := findRBACManifest(t, manifests, "ClusterRole", "manager-role")
	requireRBACRule(t, manager, "apps", "daemonsets", []string{"get", "list", "watch", "create", "update", "patch", "delete"})
	requireRBACRule(t, manager, "", "nodes", []string{"get", "list", "watch"})
	requireRBACRule(t, manager, "", "configmaps", []string{"get", "list", "watch"})

	leaderElection := findRBACManifest(t, manifests, "Role", "leader-election-role")
	require.Equal(t, "system", leaderElection.Metadata.Namespace)
	requireRBACRule(t, leaderElection, "coordination.k8s.io", "leases", []string{"get", "list", "watch", "create", "update", "patch", "delete"})
	requireRBACRule(t, leaderElection, "", "configmaps", []string{"get", "list", "watch", "create", "update", "patch", "delete"})
	requireRBACRule(t, leaderElection, "", "events", []string{"create", "patch"})

	metricsAuth := findRBACManifest(t, manifests, "ClusterRole", "metrics-auth-role")
	requireRBACRule(t, metricsAuth, "authentication.k8s.io", "tokenreviews", []string{"create"})
	requireRBACRule(t, metricsAuth, "authorization.k8s.io", "subjectaccessreviews", []string{"create"})

	metricsReader := findRBACManifest(t, manifests, "ClusterRole", "metrics-reader")
	require.Len(t, metricsReader.Rules, 1)
	require.Equal(t, []string{"/metrics"}, metricsReader.Rules[0].NonResourceURLs)
}

func projectRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "could not locate project root (go.mod) from %q", dir)
		dir = parent
	}
}
