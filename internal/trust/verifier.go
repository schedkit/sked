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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"google.golang.org/protobuf/encoding/protojson"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
)

const (
	bundleMediaTypeV03 = "application/vnd.dev.sigstore.bundle.v0.3+json"
	bundleMediaTypeV02 = "application/vnd.dev.sigstore.bundle.v0.2+json"

	cosignSignPredicateType = "https://sigstore.dev/cosign/sign/v1"

	trustedRootTTL = 24 * time.Hour
	maxBundleSize  = 1 << 20
)

type Verifier interface {
	Verify(ctx context.Context, imageRef string) error
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

func (v *CosignVerifier) Verify(ctx context.Context, imageRef string) error {
	ref, err := name.ParseReference(imageRef, name.WeakValidation)
	if err != nil {
		return fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}

	digest, err := v.resolveDigest(ctx, ref)
	if err != nil {
		return err
	}

	opts := append([]remote.Option{remote.WithContext(ctx)}, v.remoteOpt...)
	bundles := v.findBundles(ref.Context().Digest(digest.String()), digest, opts)
	if len(bundles) == 0 {
		return fmt.Errorf("no Sigstore signature bundle found for %s@%s", ref.Context().Name(), digest)
	}

	trustedRoot, err := v.trustedMaterial()
	if err != nil {
		return fmt.Errorf("load Sigstore trusted root: %w", err)
	}

	identity, err := verify.NewShortCertificateIdentity(
		v.cosign.Issuer, "",
		v.cosign.Identity, v.cosign.IdentityRegexp,
	)
	if err != nil {
		return fmt.Errorf("build certificate identity: %w", err)
	}

	entityVerifier, err := verify.NewSignedEntityVerifier(trustedRoot,
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("build Sigstore verifier: %w", err)
	}

	verifyErrs := make([]error, 0, len(bundles))
	for _, pb := range bundles {
		entity, err := bundle.NewBundle(pb)
		if err != nil {
			verifyErrs = append(verifyErrs, err)
			continue
		}
		if _, err := entityVerifier.Verify(entity, verify.NewPolicy(
			verify.WithoutArtifactUnsafe(),
			verify.WithCertificateIdentity(identity),
		)); err != nil {
			verifyErrs = append(verifyErrs, err)
			continue
		}
		return nil
	}

	return fmt.Errorf("no trusted signature for %s@%s: %w", ref.Context().Name(), digest, errors.Join(verifyErrs...))
}

func (v *CosignVerifier) resolveDigest(ctx context.Context, ref name.Reference) (v1.Hash, error) {
	if digestRef, ok := ref.(name.Digest); ok {
		digest, err := v1.NewHash(digestRef.DigestStr())
		if err != nil {
			return v1.Hash{}, fmt.Errorf("parse digest %q: %w", digestRef.DigestStr(), err)
		}
		return digest, nil
	}
	opts := append([]remote.Option{remote.WithContext(ctx)}, v.remoteOpt...)
	desc, err := remote.Head(ref, opts...)
	if err != nil {
		return v1.Hash{}, fmt.Errorf("resolve image %q: %w", ref.Name(), err)
	}
	return desc.Digest, nil
}

func (v *CosignVerifier) findBundles(digestRef name.Digest, digest v1.Hash, opts []remote.Option) []*protobundle.Bundle {
	bundles := make([]*protobundle.Bundle, 0, 1)

	appendFromImage := func(img v1.Image) {
		layers, err := img.Layers()
		if err != nil {
			return
		}
		for _, layer := range layers {
			mt, err := layer.MediaType()
			if err != nil || !isBundleMediaType(string(mt)) {
				continue
			}
			rc, err := layer.Uncompressed()
			if err != nil {
				continue
			}
			raw, readErr := io.ReadAll(io.LimitReader(rc, maxBundleSize))
			_ = rc.Close()
			if readErr != nil {
				continue
			}
			var pb protobundle.Bundle
			if err := protojson.Unmarshal(raw, &pb); err != nil {
				continue
			}
			if !bundleMatchesDigest(&pb, digest) {
				continue
			}
			bundles = append(bundles, &pb)
		}
	}

	if idx, err := remote.Referrers(digestRef, opts...); err == nil {
		if manifest, err := idx.IndexManifest(); err == nil {
			for _, m := range manifest.Manifests {
				img, err := remote.Image(digestRef.Context().Digest(m.Digest.String()), opts...)
				if err != nil {
					continue
				}
				appendFromImage(img)
			}
		}
	}

	if len(bundles) == 0 {
		tag, err := name.NewTag(fmt.Sprintf("%s:%s-%s.sig", digestRef.Context().Name(), digest.Algorithm, digest.Hex))
		if err == nil {
			if img, err := remote.Image(tag, opts...); err == nil {
				appendFromImage(img)
			}
		}
	}

	return bundles
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

func isBundleMediaType(mediaType string) bool {
	return mediaType == bundleMediaTypeV03 || mediaType == bundleMediaTypeV02
}

type intotoStatement struct {
	PredicateType string `json:"predicateType"`
	Subject       []struct {
		Digest map[string]string `json:"digest"`
	} `json:"subject"`
}

func bundleMatchesDigest(pb *protobundle.Bundle, digest v1.Hash) bool {
	dsse := pb.GetDsseEnvelope()
	if dsse == nil {
		return false
	}
	var stmt intotoStatement
	if err := json.Unmarshal(dsse.GetPayload(), &stmt); err != nil {
		return false
	}
	if stmt.PredicateType != cosignSignPredicateType {
		return false
	}
	for _, subject := range stmt.Subject {
		if subject.Digest[digest.Algorithm] == digest.Hex {
			return true
		}
	}
	return false
}
