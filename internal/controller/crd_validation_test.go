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

package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	skedv1 "github.com/schedkit/sked/api/v1"
)

func TestSchedulerReferenceSchemaValidation(t *testing.T) {
	ctx, k8sClient := newTestEnv(t)

	digest := "sha256:" + strings.Repeat("0", 64)

	valid := []string{
		"ghcr.io/schedkit/scx_rusty:latest",
		"ghcr.io/schedkit/scx_rusty",
		"ghcr.io/schedkit/scx_rusty@" + digest,
		"ghcr.io/schedkit/scx_rusty:latest@" + digest,
		"localhost:5000/schedkit/scx_rusty:v1.2.3",
		"scx_rusty:latest",
	}
	for i, image := range valid {
		scx := &skedv1.Scheduler{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("valid-reference-%d", i), Namespace: "default"},
			Spec:       skedv1.SchedulerSpec{Sched: image},
		}
		require.NoError(t, k8sClient.Create(ctx, scx), "expected %q to be accepted", image)
	}

	invalid := []string{
		"",
		"ghcr.io/",
		"ghcr.io/schedkit/scx_rusty:",
		"ghcr.io/schedkit/scx_rusty@sha256:abcd",
		"ghcr.io/schedkit/scx_rusty@sha512:" + strings.Repeat("0", 64),
		"ghcr.io/SchedKit/Scx_Rusty:latest",
		"ghcr.io/schedkit/scx_rusty:latest:extra",
		"not a reference",
	}
	for i, image := range invalid {
		scx := &skedv1.Scheduler{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("invalid-reference-%d", i), Namespace: "default"},
			Spec:       skedv1.SchedulerSpec{Sched: image},
		}
		err := k8sClient.Create(ctx, scx)
		require.Error(t, err, "expected %q to be rejected", image)
		require.ErrorContains(t, err, "spec.sched")
	}
}
