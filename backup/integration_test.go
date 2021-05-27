package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/cybozu-go/moco/pkg/bkop"
	"github.com/cybozu-go/moco/pkg/constants"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Backup/Restore", func() {
	var workDir, workDir2 string
	var bc *mockBucket
	var ops []*mockOperator
	ctx := context.Background()

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "")
		Expect(err).NotTo(HaveOccurred())
		workDir = dir
		dir, err = os.MkdirTemp("", "")
		Expect(err).NotTo(HaveOccurred())
		workDir2 = dir

		bc = &mockBucket{contents: map[string][]byte{}}
		ops = nil

		cluster := &mocov1beta1.MySQLCluster{}
		cluster.Namespace = "test"
		cluster.Name = "single"
		cluster.Spec.Replicas = 3
		cluster.Spec.PodTemplate.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "mysql"}}
		cluster.Spec.VolumeClaimTemplates = []mocov1beta1.PersistentVolumeClaim{{
			ObjectMeta: mocov1beta1.ObjectMeta{Name: "mysql-data"},
		}}
		err = k8sClient.Create(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		target := &mocov1beta1.MySQLCluster{}
		target.Namespace = "restore"
		target.Name = "target"
		target.Spec.Replicas = 1
		target.Spec.PodTemplate.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "mysql"}}
		target.Spec.VolumeClaimTemplates = []mocov1beta1.PersistentVolumeClaim{{
			ObjectMeta: mocov1beta1.ObjectMeta{Name: "mysql-data"},
		}}
		err = k8sClient.Create(ctx, target)
		Expect(err).NotTo(HaveOccurred())

		for i := 0; i < 3; i++ {
			pod := &corev1.Pod{}
			pod.Namespace = "test"
			pod.Name = cluster.PodName(i)
			pod.Labels = map[string]string{
				constants.LabelAppName:      constants.AppNameMySQL,
				constants.LabelAppInstance:  "single",
				constants.LabelAppCreatedBy: constants.AppCreator,
			}
			pod.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "mysql"}}
			err := k8sClient.Create(ctx, pod)
			Expect(err).NotTo(HaveOccurred())

			pod.Status.Conditions = []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}}
			pod.Status.PodIP = fmt.Sprintf("10.1.2.%d", i)
			err = k8sClient.Status().Update(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
		}

		pod := &corev1.Pod{}
		pod.Namespace = target.Namespace
		pod.Name = target.PodName(0)
		pod.Spec.Containers = []corev1.Container{{Name: "mysqld", Image: "mysql"}}
		err = k8sClient.Create(ctx, pod)
		Expect(err).NotTo(HaveOccurred())
		pod.Status.PodIP = "10.2.1.0"
		err = k8sClient.Status().Update(ctx, pod)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(workDir)
		os.RemoveAll(workDir2)
		for _, op := range ops {
			Expect(op.closed).To(BeTrue())
			Expect(op.prepared).To(Equal(op.finished))
		}
		k8sClient.DeleteAllOf(ctx, &mocov1beta1.MySQLCluster{}, client.InNamespace("test"))
		k8sClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace("test"))
		k8sClient.DeleteAllOf(ctx, &corev1.Event{}, client.InNamespace("test"))
		k8sClient.DeleteAllOf(ctx, &mocov1beta1.MySQLCluster{}, client.InNamespace("restore"))
		k8sClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace("restore"))
		k8sClient.DeleteAllOf(ctx, &corev1.Event{}, client.InNamespace("restore"))
	})

	It("should take a full backup and be able to restore it", func() {
		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs: []string{"binlog.000001"},
				uuid:    "123",
				gtid:    "gtid1",

				writable: true,
			}
			ops = append(ops, op)
			return op, nil
		}

		bm, err := NewBackupManager(cfg, bc, workDir, "test", "single", "", 3)
		Expect(err).NotTo(HaveOccurred())

		err = bm.Backup(ctx)
		Expect(err).NotTo(HaveOccurred())

		events := &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		Expect(events.Items).To(HaveLen(1))

		Expect(bc.contents).To(HaveLen(1))

		cluster := &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "single"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		bs := &cluster.Status.Backup
		Expect(bs.Time.IsZero()).To(BeFalse())
		Expect(bs.Elapsed.Seconds()).To(BeNumerically(">", 0))
		Expect(bs.SourceIndex).To(Equal(1))
		Expect(bs.SourceUUID).To(Equal("123"))
		Expect(bs.BinlogFilename).To(Equal("binlog.000001"))
		Expect(bs.GTIDSet).To(Equal("gtid1"))
		Expect(bs.DumpSize).To(BeNumerically(">", 0))
		Expect(bs.BinlogSize).To(BeNumerically("==", 0))
		Expect(bs.WorkDirUsage).To(BeNumerically(">", 0))
		Expect(bs.Warnings).To(BeEmpty())

		rm, err := NewRestoreManager(cfg, bc, workDir2, "test", "single", "restore", "target", "", 3, bs.Time.Time)
		Expect(err).NotTo(HaveOccurred())

		ctx2, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		err = rm.Restore(ctx2)
		Expect(err).To(MatchError(context.DeadlineExceeded))

		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs: []string{"binlog.000001"},
				uuid:    "123",
				gtid:    "gtid1",
			}
			ops = append(ops, op)
			return op, nil
		}
		err = rm.Restore(ctx)
		Expect(err).NotTo(HaveOccurred())

		events = &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("restore"))
		Expect(err).NotTo(HaveOccurred())
		Expect(events.Items).To(HaveLen(1))

		cluster = &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "restore", Name: "target"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(cluster.Status.RestoredTime).NotTo(BeNil())
	})

	It("should take an incremental backup and be able to do PiTR", func() {
		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs: []string{"binlog.000001"},
				uuid:    "123",
				gtid:    "gtid1",
			}
			ops = append(ops, op)
			return op, nil
		}

		bm, err := NewBackupManager(cfg, bc, workDir, "test", "single", "", 3)
		Expect(err).NotTo(HaveOccurred())

		err = bm.Backup(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(bc.contents).To(HaveLen(1))

		time.Sleep(1100 * time.Millisecond)
		restorePoint := time.Now()
		time.Sleep(1100 * time.Millisecond)

		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs:    []string{"binlog.000001", "binlog.000002"},
				uuid:       "123",
				gtid:       "gtid2",
				expectPiTR: true,
			}
			ops = append(ops, op)
			return op, nil
		}

		// second shot
		err = os.RemoveAll(filepath.Join(workDir, "dump"))
		Expect(err).NotTo(HaveOccurred())
		bm, err = NewBackupManager(cfg, bc, workDir, "test", "single", "", 3)
		Expect(err).NotTo(HaveOccurred())
		err = bm.Backup(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(bc.contents).To(HaveLen(3))

		cluster := &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "single"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		bs := &cluster.Status.Backup
		Expect(bs.Time.IsZero()).To(BeFalse())
		Expect(bs.SourceIndex).To(Equal(1))
		Expect(bs.SourceUUID).To(Equal("123"))
		Expect(bs.BinlogFilename).To(Equal("binlog.000002"))
		Expect(bs.GTIDSet).To(Equal("gtid2"))
		Expect(bs.DumpSize).To(BeNumerically(">", 0))
		Expect(bs.BinlogSize).To(BeNumerically(">", 0))
		Expect(bs.WorkDirUsage).To(BeNumerically(">", 0))
		Expect(bs.Warnings).To(BeEmpty())

		rm, err := NewRestoreManager(cfg, bc, workDir2, "test", "single", "restore", "target", "", 3, restorePoint)
		Expect(err).NotTo(HaveOccurred())

		err = rm.Restore(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should NOT do a PiTR when the time matches the time of a full backup", func() {
		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs: []string{"binlog.000001"},
				uuid:    "123",
				gtid:    "gtid1",
			}
			ops = append(ops, op)
			return op, nil
		}

		bm, err := NewBackupManager(cfg, bc, workDir, "test", "single", "", 3)
		Expect(err).NotTo(HaveOccurred())

		err = bm.Backup(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(bc.contents).To(HaveLen(1))

		cluster := &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "single"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		bt := cluster.Status.Backup.Time.Time

		time.Sleep(1100 * time.Millisecond)

		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs:    []string{"binlog.000001", "binlog.000002"},
				uuid:       "123",
				gtid:       "gtid2",
				expectPiTR: false,
			}
			ops = append(ops, op)
			return op, nil
		}

		// second shot
		err = os.RemoveAll(filepath.Join(workDir, "dump"))
		Expect(err).NotTo(HaveOccurred())
		bm, err = NewBackupManager(cfg, bc, workDir, "test", "single", "", 3)
		Expect(err).NotTo(HaveOccurred())
		err = bm.Backup(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(bc.contents).To(HaveLen(3))

		rm, err := NewRestoreManager(cfg, bc, workDir2, "test", "single", "restore", "target", "", 3, bt)
		Expect(err).NotTo(HaveOccurred())

		err = rm.Restore(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should record binlog backup failure", func() {
		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs: []string{"binlog.000001"},
				uuid:    "123",
				gtid:    "gtid1",
			}
			ops = append(ops, op)
			return op, nil
		}

		bm, err := NewBackupManager(cfg, bc, workDir, "test", "single", "", 3)
		Expect(err).NotTo(HaveOccurred())

		err = bm.Backup(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(bc.contents).To(HaveLen(1))

		time.Sleep(1100 * time.Millisecond)

		newOperator = func(host string, port int, user, password string, threads int) (bkop.Operator, error) {
			op := &mockOperator{
				binlogs: []string{"binlog.000002"},
				uuid:    "123",
				gtid:    "gtid2",
			}
			ops = append(ops, op)
			return op, nil
		}

		// second shot
		err = os.RemoveAll(filepath.Join(workDir, "dump"))
		Expect(err).NotTo(HaveOccurred())
		bm, err = NewBackupManager(cfg, bc, workDir, "test", "single", "", 3)
		Expect(err).NotTo(HaveOccurred())
		err = bm.Backup(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(bc.contents).To(HaveLen(2))

		events := &corev1.EventList{}
		err = k8sClient.List(ctx, events, client.InNamespace("test"))
		Expect(err).NotTo(HaveOccurred())
		Expect(events.Items).To(HaveLen(3))
		for _, ev := range events.Items {
			fmt.Println(ev.Reason)
		}

		cluster := &mocov1beta1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: "test", Name: "single"}, cluster)
		Expect(err).NotTo(HaveOccurred())
		bs := &cluster.Status.Backup
		Expect(bs.Warnings).NotTo(BeEmpty())
	})
})
