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
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	skedv1 "github.com/schedkit/sked/api/v1"
	"github.com/schedkit/sked/internal/trust"
)

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

type SchedulerValidator struct {
	Policy   trust.PolicyProvider
	Verifier trust.Verifier
}

var _ admission.Validator[*skedv1.Scheduler] = (*SchedulerValidator)(nil)

func (v *SchedulerValidator) ValidateCreate(ctx context.Context, scx *skedv1.Scheduler) (admission.Warnings, error) {
	return nil, v.validate(ctx, scx)
}

func (v *SchedulerValidator) ValidateUpdate(ctx context.Context, _, newSCX *skedv1.Scheduler) (admission.Warnings, error) {
	return nil, v.validate(ctx, newSCX)
}

func (v *SchedulerValidator) ValidateDelete(context.Context, *skedv1.Scheduler) (admission.Warnings, error) {
	return nil, nil
}

func (v *SchedulerValidator) validate(ctx context.Context, scx *skedv1.Scheduler) error {
	var (
		userInfo  authenticationv1.UserInfo
		namespace = scx.Namespace
	)
	if req, err := admission.RequestFromContext(ctx); err == nil {
		userInfo = req.UserInfo
		if req.Namespace != "" {
			namespace = req.Namespace
		}
	}

	policy := v.Policy.Get()
	if !policy.AllowsIdentity(userInfo) {
		return fmt.Errorf("identity %q is not allowed to manage Scheduler objects", userInfo.Username)
	}
	if !policy.AllowsNamespace(namespace) {
		return fmt.Errorf("namespace %q is not allowed to host Scheduler objects", namespace)
	}

	image := strings.TrimSpace(scx.Spec.Sched)
	if image == "" {
		return fmt.Errorf("spec.sched must not be empty")
	}
	if !policy.AllowsImage(image) {
		return fmt.Errorf("image %q is not in the trusted image allowlist", image)
	}
	if policy.SignatureVerificationEnabled() {
		if v.Verifier == nil {
			return fmt.Errorf("signature verification is enabled but no verifier is configured")
		}
		if _, err := v.Verifier.Verify(ctx, image); err != nil {
			return fmt.Errorf("image %q failed signature verification: %w", image, err)
		}
	}
	return nil
}

func (v *SchedulerValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &skedv1.Scheduler{}).
		WithValidator(v).
		Complete()
}
