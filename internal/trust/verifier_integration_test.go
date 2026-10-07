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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCosignVerifierIntegration(t *testing.T) {
	if os.Getenv("SKED_SIGSTORE_INTEGRATION") != "1" {
		t.Skip("set SKED_SIGSTORE_INTEGRATION=1 to run against the live schedkit images")
	}
	ctx := context.Background()
	image := "ghcr.io/schedkit/scx_rusty:latest"

	v, err := NewCosignVerifier(CosignPolicy{Issuer: DefaultSigstoreIssuer, Identity: DefaultSigstoreIdentity})
	require.NoError(t, err)
	pinned, err := v.Verify(ctx, image)
	require.NoError(t, err)
	require.Contains(t, pinned, "ghcr.io/schedkit/scx_rusty@sha256:")

	untrusted, err := NewCosignVerifier(CosignPolicy{Issuer: DefaultSigstoreIssuer, Identity: "https://example.com/not-the-signer"})
	require.NoError(t, err)
	_, err = untrusted.Verify(ctx, image)
	require.Error(t, err)
}
