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
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

const (
	DefaultConfigMapName = "scheduler-trust-policy"
	DefaultConfigMapKey  = "policy.yaml"

	DefaultSigstoreIssuer   = "https://token.actions.githubusercontent.com"
	DefaultSigstoreIdentity = "https://github.com/schedkit/plumbing/.github/workflows/oci-build-publish.yml@refs/heads/main"
)

var defaultAllowedImages = []string{
	"ghcr.io/schedkit/gthulhu",
	"ghcr.io/schedkit/scx_beerland",
	"ghcr.io/schedkit/scx_bpfland",
	"ghcr.io/schedkit/scx_cosmos",
	"ghcr.io/schedkit/scx_flash",
	"ghcr.io/schedkit/scx_flow",
	"ghcr.io/schedkit/scx_lavd",
	"ghcr.io/schedkit/scx_mitosis",
	"ghcr.io/schedkit/scx_p2dq",
	"ghcr.io/schedkit/scx_pandemonium",
	"ghcr.io/schedkit/scx_rustland",
	"ghcr.io/schedkit/scx_rusty",
	"ghcr.io/schedkit/scx_test",
	"ghcr.io/schedkit/scx_tickless",
}

type Identities struct {
	Users           []string `json:"users,omitempty"`
	Groups          []string `json:"groups,omitempty"`
	ServiceAccounts []string `json:"serviceAccounts,omitempty"`
}

type CosignPolicy struct {
	Issuer          string `json:"issuer,omitempty"`
	Identity        string `json:"identity,omitempty"`
	IdentityRegexp  string `json:"identityRegexp,omitempty"`
	TrustedRootPath string `json:"trustedRootPath,omitempty"`
}

type Policy struct {
	AllowedImages     []string     `json:"allowedImages,omitempty"`
	VerifySignatures  *bool        `json:"verifySignatures,omitempty"`
	AllowedNamespaces []string     `json:"allowedNamespaces,omitempty"`
	AllowedIdentities Identities   `json:"allowedIdentities,omitempty"`
	Cosign            CosignPolicy `json:"cosign,omitempty"`
}

func DefaultPolicy() *Policy {
	return &Policy{
		AllowedImages:    append([]string(nil), defaultAllowedImages...),
		VerifySignatures: ptr.To(true),
		Cosign: CosignPolicy{
			Issuer:   DefaultSigstoreIssuer,
			Identity: DefaultSigstoreIdentity,
		},
	}
}

func ParsePolicy(data []byte) (*Policy, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return DefaultPolicy(), nil
	}
	p := &Policy{}
	if err := yaml.UnmarshalStrict(data, p); err != nil {
		return nil, fmt.Errorf("decode trust policy: %w", err)
	}
	p.applyDefaults()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Policy) applyDefaults() {
	if p.AllowedImages == nil {
		p.AllowedImages = append([]string(nil), defaultAllowedImages...)
	}
	if p.VerifySignatures == nil {
		p.VerifySignatures = ptr.To(true)
	}
	if p.Cosign.Issuer == "" {
		p.Cosign.Issuer = DefaultSigstoreIssuer
	}
	if p.Cosign.Identity == "" && p.Cosign.IdentityRegexp == "" {
		p.Cosign.Identity = DefaultSigstoreIdentity
	}
}

func (p *Policy) SignatureVerificationEnabled() bool {
	return p.VerifySignatures == nil || *p.VerifySignatures
}

func (p *Policy) Validate() error {
	if p.SignatureVerificationEnabled() {
		if p.Cosign.Issuer == "" {
			return fmt.Errorf("cosign.issuer must not be empty when signature verification is enabled")
		}
		if p.Cosign.Identity == "" && p.Cosign.IdentityRegexp == "" {
			return fmt.Errorf("either cosign.identity or cosign.identityRegexp must be set when signature verification is enabled")
		}
	}
	if p.Cosign.IdentityRegexp != "" {
		if _, err := regexp.Compile(p.Cosign.IdentityRegexp); err != nil {
			return fmt.Errorf("cosign.identityRegexp: %w", err)
		}
	}
	for _, img := range p.AllowedImages {
		if _, err := path.Match(img, img); err != nil {
			return fmt.Errorf("allowedImages entry %q: %w", img, err)
		}
	}
	return nil
}

func (p *Policy) AllowsNamespace(namespace string) bool {
	if len(p.AllowedNamespaces) == 0 {
		return true
	}
	for _, allowed := range p.AllowedNamespaces {
		if allowed == namespace {
			return true
		}
	}
	return false
}

func (p *Policy) AllowsIdentity(info authenticationv1.UserInfo) bool {
	ids := p.AllowedIdentities
	if len(ids.Users) == 0 && len(ids.Groups) == 0 && len(ids.ServiceAccounts) == 0 {
		return true
	}
	for _, u := range ids.Users {
		if u == info.Username {
			return true
		}
	}
	for _, g := range ids.Groups {
		for _, have := range info.Groups {
			if g == have {
				return true
			}
		}
	}
	for _, sa := range ids.ServiceAccounts {
		if sa == "" {
			continue
		}
		normalized := sa
		if !strings.HasPrefix(normalized, "system:serviceaccount:") {
			normalized = "system:serviceaccount:" + normalized
		}
		if normalized == info.Username {
			return true
		}
	}
	return false
}

func (p *Policy) AllowsImage(imageRef string) bool {
	if len(p.AllowedImages) == 0 {
		return true
	}
	repo, err := imageRepository(imageRef)
	if err != nil {
		return false
	}
	for _, pattern := range p.AllowedImages {
		if ok, _ := path.Match(pattern, repo); ok {
			return true
		}
	}
	return false
}

func imageRepository(imageRef string) (string, error) {
	ref, err := name.ParseReference(imageRef, name.WeakValidation)
	if err != nil {
		return "", err
	}
	return ref.Context().Name(), nil
}
