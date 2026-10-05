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
	"os"
	"path/filepath"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestManagerClusterRoleGrantsDaemonSetAccess(t *testing.T) {
	roleFile := filepath.Join(projectRoot(t), "config", "rbac", "role.yaml")

	data, err := os.ReadFile(roleFile)
	if err != nil {
		t.Fatalf("reading generated ClusterRole %q: %v", roleFile, err)
	}

	var role struct {
		Rules []struct {
			APIGroups []string `json:"apiGroups"`
			Resources []string `json:"resources"`
			Verbs     []string `json:"verbs"`
		} `json:"rules"`
	}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096).Decode(&role); err != nil {
		t.Fatalf("decoding generated ClusterRole %q: %v", roleFile, err)
	}

	wantVerbs := []string{"get", "list", "watch", "create", "update", "patch", "delete"}

	for _, rule := range role.Rules {
		if !slices.Contains(rule.APIGroups, "apps") || !slices.Contains(rule.Resources, "daemonsets") {
			continue
		}
		for _, verb := range wantVerbs {
			if !slices.Contains(rule.Verbs, verb) {
				t.Fatalf("ClusterRole rule for apps/daemonsets is missing verb %q; got %v", verb, rule.Verbs)
			}
		}
		return
	}

	t.Fatalf("generated ClusterRole does not grant access to apps/daemonsets; " +
		"re-run `make manifests` after changing the controller RBAC markers")
}

func projectRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("determining working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate project root (go.mod) from %q", dir)
		}
		dir = parent
	}
}
