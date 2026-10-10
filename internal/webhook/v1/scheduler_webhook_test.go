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

package v1

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	admission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	skedv1 "github.com/schedkit/sked/api/v1"
	"github.com/schedkit/sked/internal/trust"
)

type staticPolicy struct{ policy *trust.Policy }

func (s staticPolicy) Get() *trust.Policy { return s.policy }

type fakeVerifier struct {
	err   error
	calls []string
}

func (f *fakeVerifier) Verify(_ context.Context, imageRef string) (string, error) {
	f.calls = append(f.calls, imageRef)
	if f.err != nil {
		return "", f.err
	}
	return imageRef, nil
}

func requestWithIdentity(namespace, username string, groups ...string) admission.Request {
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Namespace: namespace,
		UserInfo: authenticationv1.UserInfo{
			Username: username,
			Groups:   groups,
		},
	}}
}

func scheduler(namespace, image string) *skedv1.Scheduler {
	return &skedv1.Scheduler{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: namespace},
		Spec:       skedv1.SchedulerSpec{Sched: image},
	}
}

func TestValidateCreate(t *testing.T) {
	trustedImage := "ghcr.io/schedkit/scx_rusty:latest"

	t.Run("allows a trusted and signed image", func(t *testing.T) {
		verifier := &fakeVerifier{}
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: verifier}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		warnings, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		require.NoError(t, err)
		require.Empty(t, warnings)
		require.Equal(t, []string{trustedImage}, verifier.calls)
	})

	t.Run("rejects an image outside the allowlist", func(t *testing.T) {
		verifier := &fakeVerifier{}
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: verifier}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", "docker.io/library/nginx:latest"))
		require.ErrorContains(t, err, "allowlist")
		require.Empty(t, verifier.calls)
	})

	t.Run("rejects an empty image reference", func(t *testing.T) {
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", "  "))
		require.ErrorContains(t, err, "must not be empty")
	})

	t.Run("rejects an image that fails signature verification", func(t *testing.T) {
		verifier := &fakeVerifier{err: fmt.Errorf("no signatures found")}
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: verifier}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		require.ErrorContains(t, err, "signature verification")
	})

	t.Run("allows any image when verification is disabled and allowlist empty", func(t *testing.T) {
		policy := trust.DefaultPolicy()
		policy.VerifySignatures = ptr.To(false)
		policy.AllowedImages = []string{}
		v := &SchedulerValidator{Policy: staticPolicy{policy}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", "docker.io/library/nginx:latest"))
		require.NoError(t, err)
	})

	t.Run("rejects a disallowed identity", func(t *testing.T) {
		policy := trust.DefaultPolicy()
		policy.AllowedIdentities = trust.Identities{Users: []string{"alice"}}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "bob"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		require.ErrorContains(t, err, "identity")
	})

	t.Run("allows a disallowed identity check when configured for alice", func(t *testing.T) {
		policy := trust.DefaultPolicy()
		policy.AllowedIdentities = trust.Identities{Users: []string{"alice"}}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		require.NoError(t, err)
	})

	t.Run("allows an identity granted through a group", func(t *testing.T) {
		policy := trust.DefaultPolicy()
		policy.AllowedIdentities = trust.Identities{Groups: []string{"schedulers"}}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "bob", "schedulers"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		require.NoError(t, err)
	})
}

func TestValidateSpecFields(t *testing.T) {
	trustedImage := "ghcr.io/schedkit/scx_rusty:latest"

	ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))
	validator := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: &fakeVerifier{}}

	t.Run("allows node targeting, args, env, and pull settings", func(t *testing.T) {
		scx := scheduler("default", trustedImage)
		scx.Spec.NodeSelector = map[string]string{"role": "worker"}
		scx.Spec.Tolerations = []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}}
		scx.Spec.Args = []string{"--verbose"}
		scx.Spec.Env = []corev1.EnvVar{{Name: "RUST_LOG", Value: "info"}}
		scx.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "registry-creds"}}

		_, err := validator.ValidateCreate(ctx, scx)
		require.NoError(t, err)
	})

	t.Run("rejects an invalid node selector key", func(t *testing.T) {
		scx := scheduler("default", trustedImage)
		scx.Spec.NodeSelector = map[string]string{"role/": "worker"}

		_, err := validator.ValidateCreate(ctx, scx)
		require.ErrorContains(t, err, "spec.nodeSelector")
	})

	t.Run("rejects an invalid node selector value", func(t *testing.T) {
		scx := scheduler("default", trustedImage)
		scx.Spec.NodeSelector = map[string]string{"role": "not a value!"}

		_, err := validator.ValidateCreate(ctx, scx)
		require.ErrorContains(t, err, "spec.nodeSelector")
	})

	t.Run("rejects an invalid environment variable name", func(t *testing.T) {
		scx := scheduler("default", trustedImage)
		scx.Spec.Env = []corev1.EnvVar{{Name: "BAD=NAME", Value: "x"}}

		_, err := validator.ValidateCreate(ctx, scx)
		require.ErrorContains(t, err, "spec.env[0]")
	})

	t.Run("rejects duplicate environment variable names", func(t *testing.T) {
		scx := scheduler("default", trustedImage)
		scx.Spec.Env = []corev1.EnvVar{{Name: "RUST_LOG"}, {Name: "RUST_LOG"}}

		_, err := validator.ValidateCreate(ctx, scx)
		require.ErrorContains(t, err, "duplicate")
	})

	t.Run("rejects an empty image pull secret name", func(t *testing.T) {
		scx := scheduler("default", trustedImage)
		scx.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: ""}}

		_, err := validator.ValidateCreate(ctx, scx)
		require.ErrorContains(t, err, "spec.imagePullSecrets[0]")
	})
}

func TestValidateUpdate(t *testing.T) {
	v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: &fakeVerifier{}}
	ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

	_, err := v.ValidateUpdate(ctx, scheduler("default", "ghcr.io/schedkit/scx_rusty:latest"), scheduler("default", "docker.io/library/nginx:latest"))
	require.Error(t, err)
}

func TestValidateDelete(t *testing.T) {
	v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}}
	warnings, err := v.ValidateDelete(context.Background(), scheduler("default", "docker.io/library/nginx:latest"))
	require.NoError(t, err)
	require.Empty(t, warnings)
}
