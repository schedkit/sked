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

	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
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

func (f *fakeVerifier) Verify(_ context.Context, imageRef string) error {
	f.calls = append(f.calls, imageRef)
	return f.err
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
		g := NewWithT(t)
		verifier := &fakeVerifier{}
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: verifier}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		warnings, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(warnings).To(BeEmpty())
		g.Expect(verifier.calls).To(Equal([]string{trustedImage}))
	})

	t.Run("rejects an image outside the allowlist", func(t *testing.T) {
		g := NewWithT(t)
		verifier := &fakeVerifier{}
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: verifier}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", "docker.io/library/nginx:latest"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("allowlist"))
		g.Expect(verifier.calls).To(BeEmpty())
	})

	t.Run("rejects an empty image reference", func(t *testing.T) {
		g := NewWithT(t)
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", "  "))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("must not be empty"))
	})

	t.Run("rejects an image that fails signature verification", func(t *testing.T) {
		g := NewWithT(t)
		verifier := &fakeVerifier{err: fmt.Errorf("no signatures found")}
		v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: verifier}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("signature verification"))
	})

	t.Run("allows any image when verification is disabled and allowlist empty", func(t *testing.T) {
		g := NewWithT(t)
		policy := trust.DefaultPolicy()
		policy.VerifySignatures = ptr.To(false)
		policy.AllowedImages = []string{}
		v := &SchedulerValidator{Policy: staticPolicy{policy}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", "docker.io/library/nginx:latest"))
		g.Expect(err).NotTo(HaveOccurred())
	})

	t.Run("allows a scheduler in an allowed namespace", func(t *testing.T) {
		g := NewWithT(t)
		policy := trust.DefaultPolicy()
		policy.AllowedNamespaces = []string{"schedulers"}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("schedulers", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("schedulers", trustedImage))
		g.Expect(err).NotTo(HaveOccurred())
	})

	t.Run("rejects a disallowed namespace", func(t *testing.T) {
		g := NewWithT(t)
		policy := trust.DefaultPolicy()
		policy.AllowedNamespaces = []string{"schedulers"}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("namespace"))
	})

	t.Run("rejects a disallowed identity", func(t *testing.T) {
		g := NewWithT(t)
		policy := trust.DefaultPolicy()
		policy.AllowedIdentities = trust.Identities{Users: []string{"alice"}}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "bob"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("identity"))
	})

	t.Run("allows a disallowed identity check when configured for alice", func(t *testing.T) {
		g := NewWithT(t)
		policy := trust.DefaultPolicy()
		policy.AllowedIdentities = trust.Identities{Users: []string{"alice"}}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		g.Expect(err).NotTo(HaveOccurred())
	})

	t.Run("allows an identity granted through a group", func(t *testing.T) {
		g := NewWithT(t)
		policy := trust.DefaultPolicy()
		policy.AllowedIdentities = trust.Identities{Groups: []string{"schedulers"}}
		v := &SchedulerValidator{Policy: staticPolicy{policy}, Verifier: &fakeVerifier{}}
		ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "bob", "schedulers"))

		_, err := v.ValidateCreate(ctx, scheduler("default", trustedImage))
		g.Expect(err).NotTo(HaveOccurred())
	})
}

func TestValidateUpdate(t *testing.T) {
	g := NewWithT(t)
	v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}, Verifier: &fakeVerifier{}}
	ctx := admission.NewContextWithRequest(context.Background(), requestWithIdentity("default", "alice"))

	_, err := v.ValidateUpdate(ctx, scheduler("default", "ghcr.io/schedkit/scx_rusty:latest"), scheduler("default", "docker.io/library/nginx:latest"))
	g.Expect(err).To(HaveOccurred())
}

func TestValidateDelete(t *testing.T) {
	g := NewWithT(t)
	v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}}
	warnings, err := v.ValidateDelete(context.Background(), scheduler("default", "docker.io/library/nginx:latest"))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(warnings).To(BeEmpty())
}

func TestValidateCreateRejectsWrongType(t *testing.T) {
	g := NewWithT(t)
	v := &SchedulerValidator{Policy: staticPolicy{trust.DefaultPolicy()}}
	_, err := v.ValidateCreate(context.Background(), &skedv1.SchedulerList{})
	g.Expect(err).To(HaveOccurred())
}
