package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func main() {
	check := flag.Bool("check", false, "Check generated chart files without writing them")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(check bool) error {
	files, err := generate()
	if err != nil {
		return err
	}
	crds, err := filepath.Glob("charts/sked/crds/*.yaml")
	if err != nil {
		return err
	}
	for _, path := range crds {
		if _, ok := files["crds/"+filepath.Base(path)]; ok {
			continue
		}
		if check {
			return fmt.Errorf("%s is obsolete; run make helm-sync", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	for name, data := range files {
		path := filepath.Join("charts/sked", name)
		if check {
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, data) {
				return fmt.Errorf("%s is out of date; run make helm-sync", path)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func generate() (map[string][]byte, error) {
	files := map[string][]byte{}
	crds, err := filepath.Glob("config/crd/bases/*.yaml")
	if err != nil {
		return nil, err
	}
	if len(crds) == 0 {
		return nil, errors.New("no CRDs found; run from the repository root")
	}
	for _, path := range crds {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files["crds/"+filepath.Base(path)] = data
	}
	rbac := map[string]any{}
	for _, path := range []string{
		"config/rbac/role.yaml", "config/rbac/scheduler_editor_role.yaml", "config/rbac/scheduler_viewer_role.yaml",
	} {
		docs, err := readDocuments(path)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			metadata := doc["metadata"].(map[string]any)
			rbac[metadata["name"].(string)] = map[string]any{"kind": doc["kind"], "rules": doc["rules"]}
		}
	}
	files["files/rbac.yaml"], err = yaml.Marshal(rbac)
	if err != nil {
		return nil, err
	}
	webhooks, err := readDocuments("config/webhook/manifests.yaml")
	if err != nil {
		return nil, err
	}
	files["files/webhooks.yaml"], err = yaml.Marshal(map[string]any{"webhooks": webhooks[0]["webhooks"]})
	if err != nil {
		return nil, err
	}
	policy, err := readDocuments("config/trustpolicy/trust_policy.yaml")
	if err != nil {
		return nil, err
	}
	files["files/trust-policy.yaml"] = []byte(policy[0]["data"].(map[string]any)["policy.yaml"].(string))
	return files, nil
}

func readDocuments(path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var docs []map[string]any
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(doc) > 0 {
			docs = append(docs, doc)
		}
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("%s contains no resources", path)
	}
	return docs, nil
}
