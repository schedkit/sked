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
	"fmt"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	"github.com/sigstore/sigstore-go/pkg/root"
)

const trustedRootTTL = 24 * time.Hour

type Verifier interface {
	// Verify checks the signature on imageRef and returns the digest-pinned
	// reference (repo@sha256:...) of the verified content.
	Verify(ctx context.Context, imageRef string) (string, error)
}

type CosignVerifier struct {
	cosign    CosignPolicy
	remoteOpt []remote.Option

	mu          sync.Mutex
	trustedRoot root.TrustedMaterial
	fetchedAt   time.Time
}

var _ Verifier = (*CosignVerifier)(nil)

func NewCosignVerifier(policy CosignPolicy) (*CosignVerifier, error) {
	if policy.Issuer == "" {
		return nil, fmt.Errorf("cosign issuer must not be empty")
	}
	if policy.Identity == "" && policy.IdentityRegexp == "" {
		return nil, fmt.Errorf("either cosign identity or identityRegexp must be set")
	}
	return &CosignVerifier{
		cosign: policy,
		remoteOpt: []remote.Option{
			remote.WithAuthFromKeychain(authn.DefaultKeychain),
		},
	}, nil
}

func (v *CosignVerifier) Verify(ctx context.Context, imageRef string) (string, error) {
	ref, err := name.ParseReference(imageRef, name.WeakValidation)
	if err != nil {
		return "", fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}

	remoteOpts := make([]remote.Option, 0, len(v.remoteOpt)+1)
	remoteOpts = append(remoteOpts, remote.WithContext(ctx))
	remoteOpts = append(remoteOpts, v.remoteOpt...)
	ociOpts := []ociremote.Option{ociremote.WithRemoteOptions(remoteOpts...)}

	pinned, err := ociremote.ResolveDigest(ref, ociOpts...)
	if err != nil {
		return "", fmt.Errorf("resolve image %q: %w", imageRef, err)
	}

	trustedRoot, err := v.trustedMaterial()
	if err != nil {
		return "", fmt.Errorf("load Sigstore trusted root: %w", err)
	}

	// cosign v3 publishes signature bundles as DSSE envelopes behind OCI referrers.
	opts := &cosign.CheckOpts{
		RegistryClientOpts: ociOpts,
		TrustedMaterial:    trustedRoot,
		Identities: []cosign.Identity{{
			Issuer:        v.cosign.Issuer,
			Subject:       v.cosign.Identity,
			SubjectRegExp: v.cosign.IdentityRegexp,
		}},
		NewBundleFormat: true,
	}

	if _, _, err := cosign.VerifyImageAttestations(ctx, pinned, opts); err != nil {
		return "", fmt.Errorf("no trusted signature for %s: %w", pinned.Name(), err)
	}
	return pinned.Name(), nil
}

func (v *CosignVerifier) trustedMaterial() (root.TrustedMaterial, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.trustedRoot != nil && time.Since(v.fetchedAt) < trustedRootTTL {
		return v.trustedRoot, nil
	}

	var (
		trustedRoot root.TrustedMaterial
		err         error
	)
	if v.cosign.TrustedRootPath != "" {
		trustedRoot, err = root.NewTrustedRootFromPath(v.cosign.TrustedRootPath)
	} else {
		trustedRoot, err = root.FetchTrustedRoot()
	}
	if err != nil {
		return nil, err
	}
	v.trustedRoot = trustedRoot
	v.fetchedAt = time.Now()
	return trustedRoot, nil
}
