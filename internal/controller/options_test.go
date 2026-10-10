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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileOptionsWithDefaults(t *testing.T) {
	options := ReconcileOptions{}.withDefaults()

	require.Equal(t, DefaultReconcileTimeout, options.Timeout)
	require.Equal(t, DefaultMaxConcurrentReconciles, options.MaxConcurrent)
	require.Equal(t, float64(DefaultRateLimitQPS), options.RateLimitQPS)
	require.Equal(t, DefaultRateLimitBurst, options.RateLimitBurst)
	require.Equal(t, DefaultRetryBaseDelay, options.RetryBaseDelay)
	require.Equal(t, DefaultRetryMaxDelay, options.RetryMaxDelay)
}

func TestReconcileOptionsKeepsOverrides(t *testing.T) {
	options := ReconcileOptions{
		Timeout:        5 * time.Second,
		MaxConcurrent:  4,
		RateLimitQPS:   2,
		RateLimitBurst: 3,
		RetryBaseDelay: 250 * time.Millisecond,
		RetryMaxDelay:  time.Minute,
	}

	require.Equal(t, options, options.withDefaults())
}

func TestReconcileOptionsRateLimiter(t *testing.T) {
	options := ReconcileOptions{}.withDefaults()
	limiter := options.rateLimiter()
	request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "scheduler"}}

	first := limiter.When(request)
	require.Equal(t, options.RetryBaseDelay, first)

	second := limiter.When(request)
	require.Equal(t, 2*options.RetryBaseDelay, second)
	require.Equal(t, 2, limiter.NumRequeues(request))

	limiter.Forget(request)
	require.Equal(t, 0, limiter.NumRequeues(request))
	require.Equal(t, options.RetryBaseDelay, limiter.When(request))
}

func TestReconcileOptionsRateLimiterHonoursMaxDelay(t *testing.T) {
	options := ReconcileOptions{
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  4 * time.Second,
	}.withDefaults()
	limiter := options.rateLimiter()
	request := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "scheduler"}}

	for range 8 {
		limiter.When(request)
	}
	require.Equal(t, options.RetryMaxDelay, limiter.When(request))
}
