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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func LoadPolicy(ctx context.Context, reader client.Reader, namespace, name, key string) (*Policy, error) {
	if namespace == "" {
		return DefaultPolicy(), nil
	}
	if name == "" {
		name = DefaultConfigMapName
	}
	if key == "" {
		key = DefaultConfigMapKey
	}
	cm := &corev1.ConfigMap{}
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return DefaultPolicy(), nil
		}
		return nil, fmt.Errorf("read trust policy ConfigMap %s/%s: %w", namespace, name, err)
	}
	data, ok := cm.Data[key]
	if !ok {
		return nil, fmt.Errorf("trust policy ConfigMap %s/%s has no %q key", namespace, name, key)
	}
	return ParsePolicy([]byte(data))
}
