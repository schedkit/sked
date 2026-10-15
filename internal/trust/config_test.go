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
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/yaml"
)

func newFakeClient(t *testing.T, objects ...runtime.Object) *fake.ClientBuilder {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...)
}

func TestLoadPolicy(t *testing.T) {
	ctx := context.Background()

	t.Run("missing configmap falls back to defaults", func(t *testing.T) {
		c := newFakeClient(t).Build()
		p, err := LoadPolicy(ctx, c, "sked-system", "missing", "policy.yaml")
		require.NoError(t, err)
		require.Equal(t, defaultAllowedImages, p.AllowedImages)
	})

	t.Run("empty namespace falls back to defaults", func(t *testing.T) {
		c := newFakeClient(t).Build()
		p, err := LoadPolicy(ctx, c, "", "policy", "policy.yaml")
		require.NoError(t, err)
		require.Equal(t, defaultAllowedImages, p.AllowedImages)
	})

	t.Run("configmap without key returns an error", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "sked-system"},
			Data:       map[string]string{"other": "value"},
		}
		c := newFakeClient(t, cm).Build()
		_, err := LoadPolicy(ctx, c, "sked-system", "policy", "policy.yaml")
		require.Error(t, err)
	})

	t.Run("configmap policy is parsed", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "sked-system"},
			Data: map[string]string{"policy.yaml": `allowedImages:
- ghcr.io/example/*
verifySignatures: false
`},
		}
		c := newFakeClient(t, cm).Build()
		p, err := LoadPolicy(ctx, c, "sked-system", "policy", "policy.yaml")
		require.NoError(t, err)
		require.Equal(t, []string{"ghcr.io/example/*"}, p.AllowedImages)
		require.False(t, p.SignatureVerificationEnabled())
	})
}

func TestShippedConfigMapPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "trustpolicy", "trust_policy.yaml"))
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, yaml.Unmarshal(data, cm))
	p, err := ParsePolicy([]byte(cm.Data["policy.yaml"]))
	require.NoError(t, err)
	require.Equal(t, defaultAllowedImages, p.AllowedImages)
	require.True(t, p.SignatureVerificationEnabled())
}

func TestStore(t *testing.T) {
	ctx := context.Background()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "sked-system"},
		Data: map[string]string{"policy.yaml": `allowedImages:
- ghcr.io/example/*
`},
	}
	c := newFakeClient(t, cm).Build()

	s := NewStore(c, "sked-system", "policy", "policy.yaml")
	require.Equal(t, defaultAllowedImages, s.Get().AllowedImages)

	require.NoError(t, s.Refresh(ctx))
	require.Equal(t, []string{"ghcr.io/example/*"}, s.Get().AllowedImages)
}

func TestStoreRefreshesWithoutLeadership(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "sked-system"},
		Data:       map[string]string{"policy.yaml": "allowedImages: [ghcr.io/example/first]\n"},
	}
	c := newFakeClient(t, cm).Build()
	s := NewStore(c, cm.Namespace, cm.Name, "policy.yaml")
	s.interval = 10 * time.Millisecond
	var runnable manager.LeaderElectionRunnable = s
	require.False(t, runnable.NeedLeaderElection())

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- s.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-stopped)
	})
	require.Eventually(t, func() bool {
		policy := s.Get()
		return len(policy.AllowedImages) == 1 && policy.AllowedImages[0] == "ghcr.io/example/first"
	}, time.Second, 10*time.Millisecond)

	cm.Data["policy.yaml"] = "allowedImages: [ghcr.io/example/second]\n"
	require.NoError(t, c.Update(ctx, cm))
	require.Eventually(t, func() bool {
		policy := s.Get()
		return len(policy.AllowedImages) == 1 && policy.AllowedImages[0] == "ghcr.io/example/second"
	}, time.Second, 10*time.Millisecond)
}
