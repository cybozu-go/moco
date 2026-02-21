package controllers

import (
	"context"
	"fmt"
	"sync"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/clustering"
	"github.com/cybozu-go/moco/pkg/dbop"
	"github.com/cybozu-go/moco/pkg/password"
	"k8s.io/apimachinery/pkg/types"
)

type mockManager struct {
	mu       sync.Mutex
	clusters map[string]struct{}
	updated  []types.NamespacedName
}

var _ clustering.ClusterManager = &mockManager{}

func (m *mockManager) Update(key types.NamespacedName, origin string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clusters[key.String()] = struct{}{}
}

func (m *mockManager) UpdateNoStart(key types.NamespacedName, origin string) {
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

func (m *mockManager) Pause(key types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.clusters, key.String())
}

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

// mockOperatorFactory is a mock OperatorFactory for testing password rotation.
type mockOperatorFactory struct {
	mu             sync.Mutex
	rotatedUsers   map[string]string // user → newPassword (flat, for test assertions)
	discardedUsers map[string]bool
	instanceDual   map[int]map[string]bool // per-instance dual password tracking

	// rotateFailUser, if non-empty, causes RotateUserPassword to return an
	// error when called for this user name. Used to test partial failure.
	rotateFailUser string
}

var _ dbop.OperatorFactory = &mockOperatorFactory{}

func newMockOperatorFactory() *mockOperatorFactory {
	return &mockOperatorFactory{
		rotatedUsers:   make(map[string]string),
		discardedUsers: make(map[string]bool),
		instanceDual:   make(map[int]map[string]bool),
	}
}

func (f *mockOperatorFactory) setRotateFailUser(user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotateFailUser = user
}

func (f *mockOperatorFactory) New(_ context.Context, _ *mocov1beta2.MySQLCluster, _ *password.MySQLPassword, index int) (dbop.Operator, error) {
	return &mockRotationOperator{factory: f, instanceIndex: index}, nil
}

func (f *mockOperatorFactory) Cleanup() {}

func (f *mockOperatorFactory) getRotatedUsers() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make(map[string]string, len(f.rotatedUsers))
	for k, v := range f.rotatedUsers {
		result[k] = v
	}
	return result
}

func (f *mockOperatorFactory) setInstanceDual(instanceIndex int, user string, hasDual bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.instanceDual[instanceIndex] == nil {
		f.instanceDual[instanceIndex] = make(map[string]bool)
	}
	f.instanceDual[instanceIndex][user] = hasDual
}

func (f *mockOperatorFactory) getDiscardedUsers() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make(map[string]bool, len(f.discardedUsers))
	for k, v := range f.discardedUsers {
		result[k] = v
	}
	return result
}

type mockRotationOperator struct {
	factory       *mockOperatorFactory
	instanceIndex int
}

var _ dbop.Operator = &mockRotationOperator{}

func (o *mockRotationOperator) Name() string { return "mock" }
func (o *mockRotationOperator) Close() error { return nil }
func (o *mockRotationOperator) GetStatus(context.Context) (*dbop.MySQLInstanceStatus, error) {
	return nil, nil
}
func (o *mockRotationOperator) SubtractGTID(ctx context.Context, set1, set2 string) (string, error) {
	return "", nil
}
func (o *mockRotationOperator) IsSubsetGTID(ctx context.Context, set1, set2 string) (bool, error) {
	return false, nil
}
func (o *mockRotationOperator) ConfigureReplica(ctx context.Context, source dbop.AccessInfo, semisync bool) error {
	return nil
}
func (o *mockRotationOperator) ConfigurePrimary(ctx context.Context, waitForCount int) error {
	return nil
}
func (o *mockRotationOperator) StopReplicaIOThread(context.Context) error { return nil }
func (o *mockRotationOperator) WaitForGTID(ctx context.Context, gtidSet string, timeoutSeconds int) error {
	return nil
}
func (o *mockRotationOperator) SetReadOnly(context.Context, bool) error      { return nil }
func (o *mockRotationOperator) SetSuperReadOnly(context.Context, bool) error { return nil }
func (o *mockRotationOperator) KillConnections(context.Context) error        { return nil }
func (o *mockRotationOperator) GetAuthPlugin(ctx context.Context) (string, error) {
	return "caching_sha2_password", nil
}

func (o *mockRotationOperator) RotateUserPassword(ctx context.Context, user, newPassword string) error {
	o.factory.mu.Lock()
	defer o.factory.mu.Unlock()
	if o.factory.rotateFailUser == user {
		return fmt.Errorf("injected error for user %s", user)
	}
	o.factory.rotatedUsers[user] = newPassword
	if o.factory.instanceDual[o.instanceIndex] == nil {
		o.factory.instanceDual[o.instanceIndex] = make(map[string]bool)
	}
	o.factory.instanceDual[o.instanceIndex][user] = true
	return nil
}

func (o *mockRotationOperator) MigrateUserAuthPlugin(ctx context.Context, user, password, authPlugin string) error {
	return nil
}

func (o *mockRotationOperator) DiscardOldPassword(ctx context.Context, user string) error {
	o.factory.mu.Lock()
	defer o.factory.mu.Unlock()
	o.factory.discardedUsers[user] = true
	return nil
}

func (o *mockRotationOperator) HasDualPassword(ctx context.Context, user string) (bool, error) {
	o.factory.mu.Lock()
	defer o.factory.mu.Unlock()
	if o.factory.instanceDual[o.instanceIndex] == nil {
		return false, nil
	}
	return o.factory.instanceDual[o.instanceIndex][user], nil
}
