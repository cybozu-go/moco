package controllers

import (
	"context"
	"sync"

	"github.com/cybozu-go/moco/clustering"
	"k8s.io/apimachinery/pkg/types"
)

type mockManager struct {
	mu       sync.Mutex
	clusters map[string]struct{}
	updated  []types.NamespacedName
}

var _ clustering.ClusterManager = &mockManager{}

func (m *mockManager) Update(ctx context.Context, key types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clusters[key.String()] = struct{}{}
}

func (m *mockManager) UpdateNoStart(ctx context.Context, key types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updated = append(m.updated, key)
}

func (m *mockManager) Stop(key types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.clusters, key.String())
}

func (m *mockManager) StopAll() {}

func (m *mockManager) getKeys() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys := make(map[string]bool)
	for k := range m.clusters {
		keys[k] = true
	}
	return keys
}

func (m *mockManager) isUpdated(key types.NamespacedName) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range m.updated {
		if k.Namespace == key.Namespace && k.Name == key.Name {
			return true
		}
	}
	return false
}
