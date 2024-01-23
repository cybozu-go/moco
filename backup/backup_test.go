package backup

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/bkop"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type getUUIDSetMockOp struct {
	closed bool
	uuid   string
}

func (o *getUUIDSetMockOp) Ping() error {
	panic("not implemented")
}

func (o *getUUIDSetMockOp) Close() {
	o.closed = true
}

func (o *getUUIDSetMockOp) GetServerStatus(_ context.Context, st *bkop.ServerStatus) error {
	st.UUID = o.uuid
	return nil
}

func (o *getUUIDSetMockOp) DumpFull(ctx context.Context, dir string) error {
	panic("not implemented")
}

func (o *getUUIDSetMockOp) GetBinlogs(_ context.Context) ([]string, error) {
	panic("not implemented")
}

func (o *getUUIDSetMockOp) DumpBinlog(ctx context.Context, dir string, binlogName string, filterGTID string) error {
	panic("not implemented")
}

func (o *getUUIDSetMockOp) PrepareRestore(_ context.Context) error {
	panic("not implemented")
}

func (o *getUUIDSetMockOp) LoadDump(ctx context.Context, dir string) error {
	panic("not implemented")
}

func (o *getUUIDSetMockOp) LoadBinlog(ctx context.Context, binlogDir, tmpDir string, restorePoint time.Time) error {
	panic("not implemented")
}

func (o *getUUIDSetMockOp) FinishRestore(_ context.Context) error {
	panic("not implemented")
}

func makePod(ready bool) *corev1.Pod {
	pod := &corev1.Pod{}
	if !ready {
		return pod
	}
	pod.Status.Conditions = []corev1.PodCondition{{
		Type:   corev1.PodReady,
		Status: corev1.ConditionTrue,
	}}
	return pod
}

func makePod1(ready bool) []*corev1.Pod {
	return []*corev1.Pod{makePod(ready)}
}

func makePod3(ready0, ready1, ready2 bool) []*corev1.Pod {
	return []*corev1.Pod{makePod(ready0), makePod(ready1), makePod(ready2)}
}

func TestGetUUIDSet(t *testing.T) {
	var lastOp *getUUIDSetMockOp
	newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
		lastOp = &getUUIDSetMockOp{uuid: "123"}
		return lastOp, nil
	}

	testCases := []struct {
		name string
		pods []*corev1.Pod

		expected map[string]string
	}{
		{
			name:     "empty",
			pods:     []*corev1.Pod{},
			expected: map[string]string{},
		},
		{
			name:     "single-not-ready",
			pods:     makePod1(false),
			expected: map[string]string{},
		},
		{
			name:     "single-ready",
			pods:     makePod1(true),
			expected: map[string]string{"0": "123"},
		},
		{
			name:     "triple-not-ready",
			pods:     makePod3(false, false, false),
			expected: map[string]string{},
		},
		{
			name:     "triple-1st-ready",
			pods:     makePod3(true, false, false),
			expected: map[string]string{"0": "123"},
		},
		{
			name:     "triple-2nd-ready",
			pods:     makePod3(false, true, false),
			expected: map[string]string{"1": "123"},
		},
		{
			name:     "triple-3rd-ready",
			pods:     makePod3(false, false, true),
			expected: map[string]string{"2": "123"},
		},
		{
			name:     "triple-all-ready",
			pods:     makePod3(true, true, true),
			expected: map[string]string{"0": "123", "1": "123", "2": "123"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lastOp = nil
			bm := &BackupManager{
				cluster: &mocov1beta2.MySQLCluster{},
				status: bkop.ServerStatus{
					UUID: "123",
				},
			}
			uuids, err := bm.GetUUIDSet(context.Background(), tc.pods)
			if lastOp != nil && !lastOp.closed {
				t.Error("op was not closed")
			}
			if err != nil {
				t.Error("unexpected error", err)
			}
			if !reflect.DeepEqual(uuids, tc.expected) {
				t.Errorf("unexpected uuids %v, expected %v", uuids, tc.expected)
			}
		})

	}
}

func TestGetIdxsWithUnchangedUUID(t *testing.T) {
	testCases := []struct {
		name    string
		current map[string]string
		last    map[string]string

		expectIdxs []int
	}{
		{
			name:       "last-empty",
			current:    map[string]string{"0": "uuid-0"},
			last:       map[string]string{},
			expectIdxs: []int{},
		},
		{
			name:       "single-uuid-not-changed",
			current:    map[string]string{"0": "uuid-0"},
			last:       map[string]string{"0": "uuid-0"},
			expectIdxs: []int{0},
		},
		{
			name:       "single-uuid-changed",
			current:    map[string]string{"0": "uuid-0"},
			last:       map[string]string{"0": "uuid-a"},
			expectIdxs: []int{},
		},
		{
			name:       "triple-uuid-not-changed",
			current:    map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"},
			last:       map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"},
			expectIdxs: []int{0, 1, 2},
		},
		{
			name:       "triple-some-uuid-changed",
			current:    map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"},
			last:       map[string]string{"0": "uuid-0", "1": "uuid-a", "2": "uuid-2"},
			expectIdxs: []int{0, 2},
		},
		{
			name:       "triple-all-uuid-changed",
			current:    map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"},
			last:       map[string]string{"0": "uuid-a", "1": "uuid-b", "2": "uuid-c"},
			expectIdxs: []int{},
		},
		{
			name:       "triple-some-uuid-changed-or-not-exist-1",
			current:    map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"},
			last:       map[string]string{"0": "uuid-a", "2": "uuid-2"},
			expectIdxs: []int{2},
		},
		{
			name:       "triple-some-uuid-changed-or-not-exist-2",
			current:    map[string]string{"0": "uuid-0", "2": "uuid-2"},
			last:       map[string]string{"0": "uuid-a", "1": "uuid-1", "2": "uuid-2"},
			expectIdxs: []int{2},
		},
		{
			name:       "triple-with-invalid-index",
			current:    map[string]string{"0": "uuid-0", "1": "uuid-1", "hoge": "uuid-2"},
			last:       map[string]string{"0": "uuid-0", "fuga": "uuid-1", "2": "uuid-2"},
			expectIdxs: []int{0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			idxs := getIdxsWithUnchangedUUID(tc.current, tc.last)
			if !reflect.DeepEqual(idxs, tc.expectIdxs) {
				t.Errorf("unexpected indexes %v, expected %v", idxs, tc.expectIdxs)
			}
		})
	}
}

func TestChoosePod(t *testing.T) {
	makeBM := func(replicas, current int, bkup mocov1beta2.BackupStatus, pods []*corev1.Pod) *BackupManager {
		cluster := &mocov1beta2.MySQLCluster{}
		cluster.Spec.Replicas = int32(replicas)
		cluster.Status.CurrentPrimaryIndex = current
		cluster.Status.Backup = bkup
		uuidSet := make(map[string]string)
		for i, pod := range pods {
			for _, c := range pod.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					uuidSet[strconv.Itoa(i)] = "uuid-" + strconv.Itoa(i)
				}
			}
		}

		return &BackupManager{
			log:     logr.Discard(),
			cluster: cluster,
			uuidSet: uuidSet,
		}
	}

	makeBS := func(sourceIdx int, sourceUUID string, uuidSet map[string]string) mocov1beta2.BackupStatus {
		return mocov1beta2.BackupStatus{
			Time:        metav1.Now(),
			SourceIndex: sourceIdx,
			SourceUUID:  sourceUUID,
			UUIDSet:     uuidSet,
		}
	}

	testCases := []struct {
		name     string
		replicas int
		current  int
		bkup     mocov1beta2.BackupStatus
		pods     []*corev1.Pod

		err            error
		expectIdx      int
		doBackupBinlog bool
		warnings       int
	}{
		{
			name:           "single",
			replicas:       1,
			current:        0,
			bkup:           mocov1beta2.BackupStatus{},
			pods:           makePod1(true),
			err:            nil,
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       0,
		},
		{
			name:           "single-not-ready",
			replicas:       1,
			current:        0,
			bkup:           mocov1beta2.BackupStatus{},
			pods:           makePod1(false),
			err:            errors.New("no ready pod exists"),
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       0,
		},
		{
			name:           "triple-ready",
			replicas:       3,
			current:        0,
			bkup:           mocov1beta2.BackupStatus{},
			pods:           makePod3(true, false, true),
			err:            nil,
			expectIdx:      2,
			doBackupBinlog: false,
			warnings:       0,
		},
		{
			name:           "triple-some-not-ready",
			replicas:       3,
			current:        1,
			bkup:           mocov1beta2.BackupStatus{},
			pods:           makePod3(false, true, false),
			err:            nil,
			expectIdx:      1,
			doBackupBinlog: false,
			warnings:       0,
		},
		{
			name:           "triple-all-not-ready",
			replicas:       3,
			current:        1,
			bkup:           mocov1beta2.BackupStatus{},
			pods:           makePod3(false, false, false),
			err:            errors.New("no ready pod exists"),
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       0,
		},
		{
			name:           "triple-last-uuid-set-is-empty",
			replicas:       3,
			current:        1,
			bkup:           makeBS(2, "uuid-2", map[string]string{}),
			pods:           makePod3(true, true, true),
			err:            nil,
			expectIdx:      2,
			doBackupBinlog: true,
			warnings:       0,
		},
		{
			name:           "triple-last-uuid-set-is-empty-source-uuid-changed",
			replicas:       3,
			current:        1,
			bkup:           makeBS(2, "uuid-b", map[string]string{}),
			pods:           makePod3(true, true, true),
			err:            nil,
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       1,
		},
		{
			name:           "single-2nd",
			replicas:       1,
			current:        0,
			bkup:           makeBS(0, "uuid-0", map[string]string{"0": "uuid-0"}),
			pods:           makePod1(true),
			err:            nil,
			expectIdx:      0,
			doBackupBinlog: true,
			warnings:       0,
		},
		{
			name:           "single-2nd-uuid-changed",
			replicas:       1,
			current:        0,
			bkup:           makeBS(0, "uuid-a", map[string]string{"0": "uuid-a"}),
			pods:           makePod1(true),
			err:            nil,
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       1,
		},
		{
			name:           "single-2nd-not-ready",
			replicas:       1,
			current:        0,
			bkup:           makeBS(0, "uuid-0", map[string]string{"0": "uuid-0"}),
			pods:           makePod1(false),
			err:            errors.New("no ready pod exists"),
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       1,
		},
		{
			name:           "triple-2nd",
			replicas:       3,
			current:        0,
			bkup:           makeBS(1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}),
			pods:           makePod3(true, true, true),
			err:            nil,
			expectIdx:      1,
			doBackupBinlog: true,
			warnings:       0,
		},
		{
			name:           "triple-2nd-uuid-changed",
			replicas:       3,
			current:        0,
			bkup:           makeBS(1, "uuid-b", map[string]string{"0": "uuid-0", "1": "uuid-b", "2": "uuid-2"}),
			pods:           makePod3(true, true, true),
			err:            nil,
			expectIdx:      2,
			doBackupBinlog: true,
			warnings:       0,
		},
		{
			name:           "triple-2nd-last-index-not-ready",
			replicas:       3,
			current:        0,
			bkup:           makeBS(1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}),
			pods:           makePod3(true, false, true),
			err:            nil,
			expectIdx:      2,
			doBackupBinlog: true,
			warnings:       0,
		},
		{
			name:           "triple-2nd-primary",
			replicas:       3,
			current:        1,
			bkup:           makeBS(1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}),
			pods:           makePod3(true, true, true),
			err:            nil,
			expectIdx:      0,
			doBackupBinlog: true,
			warnings:       0,
		},
		{
			name:           "triple-2nd-all-not-ready",
			replicas:       3,
			current:        0,
			bkup:           makeBS(1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}),
			pods:           makePod3(false, false, false),
			err:            errors.New("no ready pod exists"),
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       1,
		},
		{
			name:           "triple-2nd-replica-uuid-changed",
			replicas:       3,
			current:        0,
			bkup:           makeBS(1, "uuid-a", map[string]string{"0": "uuid-0", "1": "uuid-a", "2": "uuid-b"}),
			pods:           makePod3(true, true, true),
			err:            nil,
			expectIdx:      0,
			doBackupBinlog: true,
			warnings:       0,
		},
		{
			name:           "triple-2nd-some-not-ready-and-uuid-changed-1",
			replicas:       3,
			current:        0,
			bkup:           makeBS(1, "uuid-1", map[string]string{"0": "uuid-a", "1": "uuid-1", "2": "uuid-b"}),
			pods:           makePod3(true, false, true),
			err:            nil,
			expectIdx:      2,
			doBackupBinlog: false,
			warnings:       1,
		},
		{
			name:           "triple-2nd-some-not-ready-and-uuid-changed-2",
			replicas:       3,
			current:        0,
			bkup:           makeBS(0, "uuid-a", map[string]string{"0": "uuid-a", "1": "uuid-1", "2": "uuid-b"}),
			pods:           makePod3(true, false, false),
			err:            nil,
			expectIdx:      0,
			doBackupBinlog: false,
			warnings:       1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bm := makeBM(tc.replicas, tc.current, tc.bkup, tc.pods)
			idx, doBackupBinlog, err := bm.ChoosePod(context.Background(), tc.pods)
			if err != nil {
				if errors.Is(err, tc.err) {
					t.Error("unexpected error", err)
					return
				}
			}
			if idx != tc.expectIdx {
				t.Errorf("unexpected index %d, expected %d", idx, tc.expectIdx)
			}
			if doBackupBinlog != tc.doBackupBinlog {
				t.Errorf("unexpected doBackupBinlog %v, expected %v", doBackupBinlog, tc.doBackupBinlog)
			}
			if len(bm.warnings) != tc.warnings {
				t.Errorf("unexpected warnings %d, expected %d", len(bm.warnings), tc.warnings)
			}
		})
	}
}
