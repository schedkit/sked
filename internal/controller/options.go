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
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	DefaultReconcileTimeout        = time.Minute
	DefaultMaxConcurrentReconciles = 2
	DefaultRateLimitQPS            = 5
	DefaultRateLimitBurst          = 10
	DefaultRetryBaseDelay          = time.Second
	DefaultRetryMaxDelay           = 5 * time.Minute
)

type ReconcileOptions struct {
	Timeout        time.Duration
	MaxConcurrent  int
	RateLimitQPS   float64
	RateLimitBurst int
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
}

func (o ReconcileOptions) withDefaults() ReconcileOptions {
	if o.Timeout <= 0 {
		o.Timeout = DefaultReconcileTimeout
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = DefaultMaxConcurrentReconciles
	}
	if o.RateLimitQPS <= 0 {
		o.RateLimitQPS = DefaultRateLimitQPS
	}
	if o.RateLimitBurst <= 0 {
		o.RateLimitBurst = DefaultRateLimitBurst
	}
	if o.RetryBaseDelay <= 0 {
		o.RetryBaseDelay = DefaultRetryBaseDelay
	}
	if o.RetryMaxDelay <= 0 {
		o.RetryMaxDelay = DefaultRetryMaxDelay
	}
	return o
}

func (o ReconcileOptions) rateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	return workqueue.NewTypedMaxOfRateLimiter[reconcile.Request](
		workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](o.RetryBaseDelay, o.RetryMaxDelay),
		&workqueue.TypedBucketRateLimiter[reconcile.Request]{
			Limiter: rate.NewLimiter(rate.Limit(o.RateLimitQPS), o.RateLimitBurst),
		},
	)
}
