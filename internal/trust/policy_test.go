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
	"testing"

	"github.com/google/go-containerregistry/pkg/v1"
	. "github.com/onsi/gomega"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protodsse "github.com/sigstore/protobuf-specs/gen/pb-go/dsse"
	authenticationv1 "k8s.io/api/authentication/v1"
)

const testDigest = "sha256:a99edef75a7c06570d00bb625c358e17ac2c610d3fa2e838a1e7264e05078e11"

func TestDefaultPolicy(t *testing.T) {
	g := NewWithT(t)
	p := DefaultPolicy()

	g.Expect(p.SignatureVerificationEnabled()).To(BeTrue())
	g.Expect(p.AllowsImage("ghcr.io/schedkit/scx_rusty:latest")).To(BeTrue())
	g.Expect(p.AllowsImage("ghcr.io/schedkit/scx_rusty@" + testDigest)).To(BeTrue())
	g.Expect(p.AllowsImage("docker.io/library/nginx:latest")).To(BeFalse())
	g.Expect(p.AllowsNamespace("anything")).To(BeTrue())
	g.Expect(p.AllowsIdentity(authenticationv1.UserInfo{Username: "alice"})).To(BeTrue())
}

func TestAllowsImage(t *testing.T) {
	g := NewWithT(t)

	g.Expect((&Policy{}).AllowsImage("docker.io/library/nginx:latest")).To(BeTrue())

	p := &Policy{AllowedImages: []string{"ghcr.io/schedkit/*"}}
	g.Expect(p.AllowsImage("ghcr.io/schedkit/scx_rusty:latest")).To(BeTrue())
	g.Expect(p.AllowsImage("ghcr.io/other/scx_rusty:latest")).To(BeFalse())
	g.Expect(p.AllowsImage("ghcr.io/schedkit/nested/scx_rusty:latest")).To(BeFalse())
	g.Expect(p.AllowsImage("not a reference")).To(BeFalse())
}

func TestAllowsNamespace(t *testing.T) {
	g := NewWithT(t)

	g.Expect((&Policy{}).AllowsNamespace("default")).To(BeTrue())

	p := &Policy{AllowedNamespaces: []string{"sked-system", "schedulers"}}
	g.Expect(p.AllowsNamespace("schedulers")).To(BeTrue())
	g.Expect(p.AllowsNamespace("default")).To(BeFalse())
}

func TestAllowsIdentity(t *testing.T) {
	g := NewWithT(t)

	g.Expect((&Policy{}).AllowsIdentity(authenticationv1.UserInfo{Username: "system:anonymous"})).To(BeTrue())

	p := &Policy{AllowedIdentities: Identities{
		Users:           []string{"alice"},
		Groups:          []string{"schedulers"},
		ServiceAccounts: []string{"sked-system:controller-manager"},
	}}
	g.Expect(p.AllowsIdentity(authenticationv1.UserInfo{Username: "alice"})).To(BeTrue())
	g.Expect(p.AllowsIdentity(authenticationv1.UserInfo{Groups: []string{"schedulers"}})).To(BeTrue())
	g.Expect(p.AllowsIdentity(authenticationv1.UserInfo{Username: "system:serviceaccount:sked-system:controller-manager"})).To(BeTrue())
	g.Expect(p.AllowsIdentity(authenticationv1.UserInfo{Username: "system:serviceaccount:default:other"})).To(BeFalse())
	g.Expect(p.AllowsIdentity(authenticationv1.UserInfo{Username: "bob"})).To(BeFalse())
}

func TestParsePolicy(t *testing.T) {
	t.Run("empty document yields defaults", func(t *testing.T) {
		g := NewWithT(t)
		p, err := ParsePolicy(nil)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(p.AllowedImages).To(Equal(defaultAllowedImages))
		g.Expect(p.SignatureVerificationEnabled()).To(BeTrue())
		g.Expect(p.Cosign.Identity).To(Equal(DefaultSigstoreIdentity))
	})

	t.Run("omitted fields inherit defaults", func(t *testing.T) {
		g := NewWithT(t)
		p, err := ParsePolicy([]byte("allowedNamespaces:\n- schedulers\n"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(p.AllowedImages).To(Equal(defaultAllowedImages))
		g.Expect(p.SignatureVerificationEnabled()).To(BeTrue())
		g.Expect(p.AllowedNamespaces).To(Equal([]string{"schedulers"}))
	})

	t.Run("verification can be disabled explicitly", func(t *testing.T) {
		g := NewWithT(t)
		p, err := ParsePolicy([]byte("verifySignatures: false\nallowedImages: []\n"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(p.SignatureVerificationEnabled()).To(BeFalse())
		g.Expect(p.AllowsImage("docker.io/library/nginx:latest")).To(BeTrue())
	})

	t.Run("unknown fields are rejected", func(t *testing.T) {
		g := NewWithT(t)
		_, err := ParsePolicy([]byte("notAField: true\n"))
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("invalid identity regexp is rejected", func(t *testing.T) {
		g := NewWithT(t)
		_, err := ParsePolicy([]byte("cosign:\n  identityRegexp: \"[\"\n"))
		g.Expect(err).To(HaveOccurred())
	})
}

func TestBundleMatchesDigest(t *testing.T) {
	g := NewWithT(t)
	digest, err := v1.NewHash(testDigest)
	g.Expect(err).NotTo(HaveOccurred())

	payload := []byte(`{"predicateType":"https://sigstore.dev/cosign/sign/v1","subject":[{"digest":{"sha256":"a99edef75a7c06570d00bb625c358e17ac2c610d3fa2e838a1e7264e05078e11"}}]}`)
	b := &protobundle.Bundle{Content: &protobundle.Bundle_DsseEnvelope{
		DsseEnvelope: &protodsse.Envelope{PayloadType: "application/vnd.in-toto+json", Payload: payload},
	}}
	g.Expect(bundleMatchesDigest(b, digest)).To(BeTrue())

	other, err := v1.NewHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(bundleMatchesDigest(b, other)).To(BeFalse())

	wrongPredicate := &protobundle.Bundle{Content: &protobundle.Bundle_DsseEnvelope{
		DsseEnvelope: &protodsse.Envelope{Payload: []byte(`{"predicateType":"https://slsa.dev/provenance/v1","subject":[{"digest":{"sha256":"a99edef75a7c06570d00bb625c358e17ac2c610d3fa2e838a1e7264e05078e11"}}]}`)},
	}}
	g.Expect(bundleMatchesDigest(wrongPredicate, digest)).To(BeFalse())

	g.Expect(bundleMatchesDigest(&protobundle.Bundle{}, digest)).To(BeFalse())
}

func TestNewCosignVerifier(t *testing.T) {
	g := NewWithT(t)

	_, err := NewCosignVerifier(CosignPolicy{Identity: "x"})
	g.Expect(err).To(HaveOccurred())

	_, err = NewCosignVerifier(CosignPolicy{Issuer: "x"})
	g.Expect(err).To(HaveOccurred())

	v, err := NewCosignVerifier(CosignPolicy{Issuer: "x", Identity: "y"})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(v).NotTo(BeNil())
}
