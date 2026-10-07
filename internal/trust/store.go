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
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const DefaultRefreshInterval = 30 * time.Second

// PolicyProvider supplies the currently active trust policy.
type PolicyProvider interface {
	Get() *Policy
}

type Store struct {
	reader    client.Reader
	namespace string
	name      string
	key       string
	interval  time.Duration

	mu     sync.RWMutex
	policy *Policy
}

var _ interface {
	PolicyProvider
	Start(context.Context) error
} = (*Store)(nil)

func NewStore(reader client.Reader, namespace, name, key string) *Store {
	if name == "" {
		name = DefaultConfigMapName
	}
	if key == "" {
		key = DefaultConfigMapKey
	}
	return &Store{
		reader:    reader,
		namespace: namespace,
		name:      name,
		key:       key,
		interval:  DefaultRefreshInterval,
		policy:    DefaultPolicy(),
	}
}

func (s *Store) Get() *Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policy
}

func (s *Store) Refresh(ctx context.Context) error {
	policy, err := LoadPolicy(ctx, s.reader, s.namespace, s.name, s.key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.policy = policy
	s.mu.Unlock()
	return nil
}

func (s *Store) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	if err := s.Refresh(ctx); err != nil {
		logger.Error(err, "unable to load trust policy, keeping previous policy")
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.Refresh(ctx); err != nil {
				logger.Error(err, "unable to refresh trust policy, keeping previous policy")
			}
		}
	}
}
