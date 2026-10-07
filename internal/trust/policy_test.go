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

	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
)

const testDigest = "sha256:a99edef75a7c06570d00bb625c358e17ac2c610d3fa2e838a1e7264e05078e11"

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()

	require.True(t, p.SignatureVerificationEnabled())
	require.True(t, p.AllowsImage("ghcr.io/schedkit/scx_rusty:latest"))
	require.True(t, p.AllowsImage("ghcr.io/schedkit/scx_rusty@"+testDigest))
	require.False(t, p.AllowsImage("docker.io/library/nginx:latest"))
	require.True(t, p.AllowsNamespace("anything"))
	require.True(t, p.AllowsIdentity(authenticationv1.UserInfo{Username: "alice"}))
}

func TestAllowsImage(t *testing.T) {
	require.True(t, (&Policy{}).AllowsImage("docker.io/library/nginx:latest"))

	p := &Policy{AllowedImages: []string{"ghcr.io/schedkit/*"}}
	require.True(t, p.AllowsImage("ghcr.io/schedkit/scx_rusty:latest"))
	require.False(t, p.AllowsImage("ghcr.io/other/scx_rusty:latest"))
	require.False(t, p.AllowsImage("ghcr.io/schedkit/nested/scx_rusty:latest"))
	require.False(t, p.AllowsImage("not a reference"))
}

func TestAllowsNamespace(t *testing.T) {
	require.True(t, (&Policy{}).AllowsNamespace("default"))

	p := &Policy{AllowedNamespaces: []string{"sked-system", "schedulers"}}
	require.True(t, p.AllowsNamespace("schedulers"))
	require.False(t, p.AllowsNamespace("default"))
}

func TestAllowsIdentity(t *testing.T) {
	require.True(t, (&Policy{}).AllowsIdentity(authenticationv1.UserInfo{Username: "system:anonymous"}))

	p := &Policy{AllowedIdentities: Identities{
		Users:           []string{"alice"},
		Groups:          []string{"schedulers"},
		ServiceAccounts: []string{"sked-system:controller-manager"},
	}}
	require.True(t, p.AllowsIdentity(authenticationv1.UserInfo{Username: "alice"}))
	require.True(t, p.AllowsIdentity(authenticationv1.UserInfo{Groups: []string{"schedulers"}}))
	require.True(t, p.AllowsIdentity(authenticationv1.UserInfo{Username: "system:serviceaccount:sked-system:controller-manager"}))
	require.False(t, p.AllowsIdentity(authenticationv1.UserInfo{Username: "system:serviceaccount:default:other"}))
	require.False(t, p.AllowsIdentity(authenticationv1.UserInfo{Username: "bob"}))
}

func TestParsePolicy(t *testing.T) {
	t.Run("empty document yields defaults", func(t *testing.T) {
		p, err := ParsePolicy(nil)
		require.NoError(t, err)
		require.Equal(t, defaultAllowedImages, p.AllowedImages)
		require.True(t, p.SignatureVerificationEnabled())
		require.Equal(t, DefaultSigstoreIdentity, p.Cosign.Identity)
	})

	t.Run("omitted fields inherit defaults", func(t *testing.T) {
		p, err := ParsePolicy([]byte("allowedNamespaces:\n- schedulers\n"))
		require.NoError(t, err)
		require.Equal(t, defaultAllowedImages, p.AllowedImages)
		require.True(t, p.SignatureVerificationEnabled())
		require.Equal(t, []string{"schedulers"}, p.AllowedNamespaces)
	})

	t.Run("verification can be disabled explicitly", func(t *testing.T) {
		p, err := ParsePolicy([]byte("verifySignatures: false\nallowedImages: []\n"))
		require.NoError(t, err)
		require.False(t, p.SignatureVerificationEnabled())
		require.True(t, p.AllowsImage("docker.io/library/nginx:latest"))
	})

	t.Run("unknown fields are rejected", func(t *testing.T) {
		_, err := ParsePolicy([]byte("notAField: true\n"))
		require.Error(t, err)
	})

	t.Run("invalid identity regexp is rejected", func(t *testing.T) {
		_, err := ParsePolicy([]byte("cosign:\n  identityRegexp: \"[\"\n"))
		require.Error(t, err)
	})
}

func TestNewCosignVerifier(t *testing.T) {
	_, err := NewCosignVerifier(CosignPolicy{Identity: "x"})
	require.Error(t, err)

	_, err = NewCosignVerifier(CosignPolicy{Issuer: "x"})
	require.Error(t, err)

	v, err := NewCosignVerifier(CosignPolicy{Issuer: "x", Identity: "y"})
	require.NoError(t, err)
	require.NotNil(t, v)
}
