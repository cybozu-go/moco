package backup

import (
	"context"
	"testing"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/bkop"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type choosePodMockOp struct {
	closed bool
	uuid   string
}

func (o *choosePodMockOp) Ping() error {
	panic("not implemented")
}

func (o *choosePodMockOp) Reconnect() error {
	panic("not implemented")
}

func (o *choosePodMockOp) Close() {
	o.closed = true
}

func (o *choosePodMockOp) GetServerStatus(_ context.Context, st *bkop.ServerStatus) error {
	st.UUID = o.uuid
	return nil
}

func (o *choosePodMockOp) DumpFull(ctx context.Context, dir string) error {
	panic("not implemented")
}

func (o *choosePodMockOp) GetBinlogs(_ context.Context) ([]string, error) {
	panic("not implemented")
}

func (o *choosePodMockOp) DumpBinlog(ctx context.Context, dir string, binlogName string, filterGTID string) error {
	panic("not implemented")
}

func (o *choosePodMockOp) PrepareRestore(_ context.Context) error {
	panic("not implemented")
}

func (o *choosePodMockOp) LoadDump(ctx context.Context, dir string) error {
	panic("not implemented")
}

func (o *choosePodMockOp) LoadBinlog(ctx context.Context, dir string, restorePoint time.Time) error {
	panic("not implemented")
}

func (o *choosePodMockOp) FinishRestore(_ context.Context) error {
	panic("not implemented")
}

func TestChoosePod(t *testing.T) {
	makePod := func(ready bool) *corev1.Pod {
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

	makePod1 := func(ready bool) []*corev1.Pod {
		return []*corev1.Pod{makePod(ready)}
	}
	makePod3 := func(ready0, ready1, ready2 bool) []*corev1.Pod {
		return []*corev1.Pod{makePod(ready0), makePod(ready1), makePod(ready2)}
	}

	var lastOp *choosePodMockOp
	newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
		lastOp = &choosePodMockOp{uuid: "123"}
		return lastOp, nil
	}

	makeBM := func(replicas, current int, bkup mocov1beta1.BackupStatus) *BackupManager {
		cluster := &mocov1beta1.MySQLCluster{}
		cluster.Spec.Replicas = int32(replicas)
		cluster.Status.CurrentPrimaryIndex = current
		cluster.Status.Backup = bkup
		return &BackupManager{
			log:     logr.Discard(),
			cluster: cluster,
		}
	}

	makeBS := func(idx int, uuid string) mocov1beta1.BackupStatus {
		return mocov1beta1.BackupStatus{
			Time:        metav1.Now(),
			SourceIndex: idx,
			SourceUUID:  uuid,
		}
	}

	testCases := []struct {
		name     string
		replicas int
		current  int
		bkup     mocov1beta1.BackupStatus
		pods     []*corev1.Pod

		expectIdx int
	}{
		{"single", 1, 0, mocov1beta1.BackupStatus{}, makePod1(true), 0},
		{"single-not-ready", 1, 0, mocov1beta1.BackupStatus{}, makePod1(false), 0},
		{"triple-ready", 3, 0, mocov1beta1.BackupStatus{}, makePod3(true, false, true), 2},
		{"triple-not-ready", 3, 1, mocov1beta1.BackupStatus{}, makePod3(false, true, false), 1},
		{"single-2nd", 1, 0, makeBS(0, "123"), makePod1(true), 0},
		{"single-2nd-uuid-changed", 1, 0, makeBS(0, "abc"), makePod1(true), 0},
		{"single-2nd-not-ready", 1, 0, makeBS(0, "123"), makePod1(false), 0},
		{"triple-2nd", 3, 0, makeBS(1, "123"), makePod3(true, true, true), 1},
		{"triple-2nd-uuid-changed", 3, 0, makeBS(1, "abc"), makePod3(true, true, true), 2},
		{"triple-2nd-not-ready", 3, 0, makeBS(1, "123"), makePod3(true, false, true), 2},
		{"triple-2nd-primary", 3, 1, makeBS(1, "123"), makePod3(true, true, true), 0},
		{"triple-2nd-all-not-ready", 3, 0, makeBS(1, "123"), makePod3(true, false, false), 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lastOp = nil
			bm := makeBM(tc.replicas, tc.current, tc.bkup)
			idx, err := bm.ChoosePod(context.Background(), tc.pods)
			if lastOp != nil && !lastOp.closed {
				t.Error("op was not closed")
			}
			if err != nil {
				t.Error("unexpected error", err)
				return
			}

			if idx != tc.expectIdx {
				t.Errorf("unexpected index %d, expected %d", idx, tc.expectIdx)
			}
		})
	}
}
