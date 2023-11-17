package backup

import (
	"context"
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
		{"empty", []*corev1.Pod{}, map[string]string{}},
		{"single-not-ready", makePod1(false), map[string]string{}},
		{"single-ready", makePod1(true), map[string]string{"0": "123"}},
		{"triple-not-ready", makePod3(false, false, false), map[string]string{}},
		{"triple-1st-ready", makePod3(true, false, false), map[string]string{"0": "123"}},
		{"triple-2nd-ready", makePod3(false, true, false), map[string]string{"1": "123"}},
		{"triple-3rd-ready", makePod3(false, false, true), map[string]string{"2": "123"}},
		{"triple-all-ready", makePod3(true, true, true), map[string]string{"0": "123", "1": "123", "2": "123"}},
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
		{"single-uuid-not-changed", map[string]string{"0": "uuid-0"}, map[string]string{"0": "uuid-0"}, []int{0}},
		{"single-uuid-changed", map[string]string{"0": "uuid-0"}, map[string]string{"0": "uuid-a"}, []int{}},
		{"triple-uuid-not-changed", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}, map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}, []int{0, 1, 2}},
		{"triple-some-uuid-changed", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}, map[string]string{"0": "uuid-0", "1": "uuid-a", "2": "uuid-2"}, []int{0, 2}},
		{"triple-all-uuid-changed", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}, map[string]string{"0": "uuid-a", "1": "uuid-b", "2": "uuid-c"}, []int{}},
		{"triple-some-uuid-changed-or-not-exist-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}, map[string]string{"0": "uuid-a", "2": "uuid-2"}, []int{2}},
		{"triple-some-uuid-changed-or-not-exist-2", map[string]string{"0": "uuid-0", "2": "uuid-2"}, map[string]string{"0": "uuid-a", "1": "uuid-1", "2": "uuid-2"}, []int{2}},
		{"triple-with-invalid-index", map[string]string{"0": "uuid-0", "1": "uuid-1", "hoge": "uuid-2"}, map[string]string{"0": "uuid-0", "fuga": "uuid-1", "2": "uuid-2"}, []int{0}},
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

	makeBS := func(replicas, idx int, uuid string, uuidSet map[string]string) mocov1beta2.BackupStatus {
		return mocov1beta2.BackupStatus{
			Time:        metav1.Now(),
			SourceIndex: idx,
			SourceUUID:  uuid,
			UUIDSet:     uuidSet,
		}
	}

	testCases := []struct {
		name     string
		replicas int
		current  int
		bkup     mocov1beta2.BackupStatus
		pods     []*corev1.Pod

		expectIdx        int
		skipBackupBinlog bool
		warnings         int
	}{
		{"single", 1, 0, mocov1beta2.BackupStatus{}, makePod1(true), 0, true, 0},
		{"single-not-ready", 1, 0, mocov1beta2.BackupStatus{}, makePod1(false), 0, true, 0},
		{"triple-ready", 3, 0, mocov1beta2.BackupStatus{}, makePod3(true, false, true), 2, true, 0},
		{"triple-not-ready", 3, 1, mocov1beta2.BackupStatus{}, makePod3(false, true, false), 1, true, 0},
		{"single-2nd", 1, 0, makeBS(1, 0, "uuid-0", map[string]string{"0": "uuid-0"}), makePod1(true), 0, false, 0},
		{"single-2nd-uuid-changed", 1, 0, makeBS(1, 0, "uuid-a", map[string]string{"0": "uuid-a"}), makePod1(true), 0, true, 1},
		{"single-2nd-not-ready", 1, 0, makeBS(1, 0, "uuid-0", map[string]string{"0": "uuid-0"}), makePod1(false), 0, true, 1},
		{"triple-2nd", 3, 0, makeBS(3, 1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}), makePod3(true, true, true), 1, false, 0},
		{"triple-2nd-uuid-changed", 3, 0, makeBS(3, 1, "uuid-b", map[string]string{"0": "uuid-0", "1": "uuid-b", "2": "uuid-2"}), makePod3(true, true, true), 2, false, 0},
		{"triple-2nd-not-ready", 3, 0, makeBS(3, 1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}), makePod3(true, false, true), 2, false, 0},
		{"triple-2nd-primary", 3, 1, makeBS(3, 1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}), makePod3(true, true, true), 0, false, 0},
		{"triple-2nd-all-not-ready", 3, 0, makeBS(3, 1, "uuid-1", map[string]string{"0": "uuid-0", "1": "uuid-1", "2": "uuid-2"}), makePod3(true, false, false), 0, false, 0},
		{"triple-2nd-all-not-ready-and-uuid-changed-1", 3, 0, makeBS(3, 0, "uuid-a", map[string]string{"0": "uuid-a", "1": "uuid-1", "2": "uuid-b"}), makePod3(true, false, true), 2, true, 1},
		{"triple-2nd-all-not-ready-and-uuid-changed-2", 3, 0, makeBS(3, 0, "uuid-a", map[string]string{"0": "uuid-a", "1": "uuid-1", "2": "uuid-b"}), makePod3(true, false, false), 0, true, 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bm := makeBM(tc.replicas, tc.current, tc.bkup, tc.pods)
			idx, skipBackupBinlog, err := bm.ChoosePod(context.Background(), tc.pods)
			if err != nil {
				t.Error("unexpected error", err)
				return
			}

			if idx != tc.expectIdx {
				t.Errorf("unexpected index %d, expected %d", idx, tc.expectIdx)
			}
			if skipBackupBinlog != tc.skipBackupBinlog {
				t.Errorf("unexpected skipBackupBinlog %v, expected %v", skipBackupBinlog, tc.skipBackupBinlog)
			}
			if len(bm.warnings) != tc.warnings {
				t.Errorf("unexpected warnings %d, expected %d", len(bm.warnings), tc.warnings)
			}
		})
	}
}
