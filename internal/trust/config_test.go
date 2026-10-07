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

package trust

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

func newFakeClient(t *testing.T, objects ...runtime.Object) *fake.ClientBuilder {
	t.Helper()
	scheme := runtime.NewScheme()
	g := NewWithT(t)
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...)
}

func TestLoadPolicy(t *testing.T) {
	ctx := context.Background()

	t.Run("missing configmap falls back to defaults", func(t *testing.T) {
		g := NewWithT(t)
		c := newFakeClient(t).Build()
		p, err := LoadPolicy(ctx, c, "sked-system", "missing", "policy.yaml")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(p.AllowedImages).To(Equal(defaultAllowedImages))
	})

	t.Run("empty namespace falls back to defaults", func(t *testing.T) {
		g := NewWithT(t)
		c := newFakeClient(t).Build()
		p, err := LoadPolicy(ctx, c, "", "policy", "policy.yaml")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(p.AllowedImages).To(Equal(defaultAllowedImages))
	})

	t.Run("configmap without key returns an error", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "sked-system"},
			Data:       map[string]string{"other": "value"},
		}
		c := newFakeClient(t, cm).Build()
		_, err := LoadPolicy(ctx, c, "sked-system", "policy", "policy.yaml")
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("configmap policy is parsed", func(t *testing.T) {
		g := NewWithT(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "sked-system"},
			Data: map[string]string{"policy.yaml": `allowedImages:
- ghcr.io/example/*
verifySignatures: false
`},
		}
		c := newFakeClient(t, cm).Build()
		p, err := LoadPolicy(ctx, c, "sked-system", "policy", "policy.yaml")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(p.AllowedImages).To(Equal([]string{"ghcr.io/example/*"}))
		g.Expect(p.SignatureVerificationEnabled()).To(BeFalse())
	})
}

func TestShippedConfigMapPolicy(t *testing.T) {
	g := NewWithT(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "trustpolicy", "trust_policy.yaml"))
	g.Expect(err).NotTo(HaveOccurred())

	cm := &corev1.ConfigMap{}
	g.Expect(yaml.Unmarshal(data, cm)).To(Succeed())
	p, err := ParsePolicy([]byte(cm.Data["policy.yaml"]))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(p.AllowedImages).To(Equal(defaultAllowedImages))
	g.Expect(p.SignatureVerificationEnabled()).To(BeTrue())
}

func TestStore(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "sked-system"},
		Data: map[string]string{"policy.yaml": `allowedImages:
- ghcr.io/example/*
`},
	}
	c := newFakeClient(t, cm).Build()

	s := NewStore(c, "sked-system", "policy", "policy.yaml")
	g.Expect(s.Get().AllowedImages).To(Equal(defaultAllowedImages))

	g.Expect(s.Refresh(ctx)).To(Succeed())
	g.Expect(s.Get().AllowedImages).To(Equal([]string{"ghcr.io/example/*"}))
}
