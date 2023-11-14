package backup

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/cybozu-go/moco/pkg/bkop"
	"github.com/cybozu-go/moco/pkg/bucket"
	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/event"
	"github.com/go-logr/logr"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/reference"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type BackupManager struct {
	log           logr.Logger
	client        client.Client
	cluster       *mocov1beta2.MySQLCluster
	clusterRef    *corev1.ObjectReference
	mysqlPassword string
	workDir       string
	bucket        bucket.Bucket
	threads       int

	// status fields
	startTime    time.Time
	sourceIndex  int
	status       bkop.ServerStatus
	gtidSet      string
	dumpSize     int64
	binlogSize   int64
	workDirUsage int64
	warnings     []string
}

func NewBackupManager(cfg *rest.Config, bc bucket.Bucket, dir, ns, name, password string, threads int) (*BackupManager, error) {
	log := zap.New(zap.WriteTo(os.Stderr), zap.StacktraceLevel(zapcore.DPanicLevel))
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := mocov1beta2.AddToScheme(scheme); err != nil {
		return nil, err
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	cluster := &mocov1beta2.MySQLCluster{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, cluster); err != nil {
		return nil, fmt.Errorf("failed to get MySQLCluster %s/%s: %w", ns, name, err)
	}

	ref, err := reference.GetReference(scheme, cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get reference for MySQLCluster: %w", err)
	}

	return &BackupManager{
		log:           log,
		client:        k8sClient,
		cluster:       cluster,
		clusterRef:    ref,
		mysqlPassword: password,
		workDir:       dir,
		bucket:        bc,
		threads:       threads,
	}, nil
}

func (bm *BackupManager) Backup(ctx context.Context) error {
	pods := &corev1.PodList{}
	if err := bm.client.List(ctx, pods, client.InNamespace(bm.cluster.Namespace), client.MatchingLabels{
		constants.LabelAppName:      constants.AppNameMySQL,
		constants.LabelAppInstance:  bm.cluster.Name,
		constants.LabelAppCreatedBy: constants.AppCreator,
	}); err != nil {
		return fmt.Errorf("failed to get pod list: %w", err)
	}

	if len(pods.Items) != int(bm.cluster.Spec.Replicas) {
		return fmt.Errorf("too few Pods for %s/%s", bm.cluster.Namespace, bm.cluster.Name)
	}

	orderedPods := make([]*corev1.Pod, bm.cluster.Spec.Replicas)
	for i, pod := range pods.Items {
		fields := strings.Split(pod.Name, "-")
		index, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			return fmt.Errorf("bad pod name: %s", pod.Name)
		}

		if index < 0 || index >= len(pods.Items) {
			return fmt.Errorf("index out of range: %d", index)
		}
		orderedPods[index] = &pods.Items[i]
	}

	sourceIndex, err := bm.ChoosePod(ctx, orderedPods)
	if err != nil {
		return fmt.Errorf("failed to choose source instance: %w", err)
	}
	bm.sourceIndex = sourceIndex

	op, err := newOperator(orderedPods[sourceIndex].Status.PodIP,
		constants.MySQLPort, constants.BackupUser, bm.mysqlPassword, bm.threads)
	if err != nil {
		return fmt.Errorf("failed to create operator: %w", err)
	}
	defer op.Close()

	if err := op.GetServerStatus(ctx, &bm.status); err != nil {
		return fmt.Errorf("failed to get server status: %w", err)
	}

	bm.startTime = time.Now().UTC()
	bm.log.Info("chosen source",
		"index", sourceIndex,
		"time", bm.startTime.Format(constants.BackupTimeFormat),
		"uuid", bm.status.UUID,
		"binlog", bm.status.CurrentBinlog)

	if err := bm.backupFull(ctx, op); err != nil {
		return fmt.Errorf("failed to take a full dump: %w", err)
	}

	// dump and upload binlog for the second or later backups
	lastBackup := &bm.cluster.Status.Backup
	if !lastBackup.Time.IsZero() {
		if err := bm.backupBinlog(ctx, op); err != nil {
			// since the full backup has succeeded, we should continue
			ev := event.BackupNoBinlog.ToEvent(bm.clusterRef)
			if err := bm.client.Create(ctx, ev); err != nil {
				bm.log.Error(err, "failed to create an event for no-binlog")
			}
			bm.log.Error(err, "failed to backup binary logs")
			bm.warnings = append(bm.warnings, fmt.Sprintf("failed to backup binary logs: %v", err))
		}
	}

	elapsed := time.Since(bm.startTime)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &mocov1beta2.MySQLCluster{}
		if err := bm.client.Get(ctx, client.ObjectKeyFromObject(bm.cluster), cluster); err != nil {
			return err
		}

		sb := &cluster.Status.Backup
		sb.Time = metav1.NewTime(bm.startTime)
		sb.Elapsed = metav1.Duration{Duration: elapsed}
		sb.SourceIndex = sourceIndex
		sb.SourceUUID = bm.status.UUID
		sb.BinlogFilename = bm.status.CurrentBinlog
		sb.GTIDSet = bm.gtidSet
		sb.DumpSize = bm.dumpSize
		sb.BinlogSize = bm.binlogSize
		sb.WorkDirUsage = bm.workDirUsage
		sb.Warnings = bm.warnings

		return bm.client.Status().Update(ctx, cluster)
	})
	if err != nil {
		return fmt.Errorf("failed to update MySQLCluster status: %w", err)
	}

	ev := event.BackupCreated.ToEvent(bm.clusterRef)
	if err := bm.client.Create(ctx, ev); err != nil {
		bm.log.Error(err, "failed to create an event for backup creation")
	}
	bm.log.Info("backup finished successfully")

	return nil
}

func (bm *BackupManager) ChoosePod(ctx context.Context, pods []*corev1.Pod) (int, error) {
	cluster := bm.cluster
	// if this is the first time
	if cluster.Status.Backup.Time.IsZero() {
		if len(pods) == 1 {
			return 0, nil
		}

		for i := range pods {
			if i == int(cluster.Status.CurrentPrimaryIndex) {
				continue
			}
			if podIsReady(pods[i]) {
				return i, nil
			}
		}
		return int(cluster.Status.CurrentPrimaryIndex), nil
	}

	lastIndex := cluster.Status.Backup.SourceIndex
	op, err := newOperator(cluster.PodHostname(lastIndex),
		constants.MySQLPort,
		constants.BackupUser,
		bm.mysqlPassword,
		bm.threads)
	if err != nil {
		return -1, err
	}
	defer op.Close()

	st := &bkop.ServerStatus{}
	if err := op.GetServerStatus(ctx, st); err != nil {
		return -1, err
	}

	if st.UUID != cluster.Status.Backup.SourceUUID {
		bm.log.Info("server_uuid of the last backup source has changed", "index", lastIndex)

		for i := range pods {
			if i == lastIndex {
				continue
			}
			if i == int(cluster.Status.CurrentPrimaryIndex) {
				continue
			}
			if podIsReady(pods[i]) {
				return i, nil
			}
		}
		return cluster.Status.CurrentPrimaryIndex, nil
	}

	if !podIsReady(pods[lastIndex]) {
		bm.log.Info("the last backup source is not ready", "index", lastIndex)

		for i := range pods {
			if i == lastIndex {
				continue
			}
			if i == int(cluster.Status.CurrentPrimaryIndex) {
				continue
			}
			if podIsReady(pods[i]) {
				return i, nil
			}
		}
		return cluster.Status.CurrentPrimaryIndex, nil
	}

	if lastIndex == int(cluster.Status.CurrentPrimaryIndex) {
		bm.log.Info("the last backup source is not a replica", "index", lastIndex)
		for i := range pods {
			if i == lastIndex {
				continue
			}
			if podIsReady(pods[i]) {
				return i, nil
			}
		}
		return cluster.Status.CurrentPrimaryIndex, nil
	}

	return lastIndex, nil
}

func (bm *BackupManager) backupFull(ctx context.Context, op bkop.Operator) error {
	dumpDir := filepath.Join(bm.workDir, "dump")
	if err := os.MkdirAll(dumpDir, 0755); err != nil {
		return fmt.Errorf("failed to make dump directory: %w", err)
	}
	defer os.RemoveAll(dumpDir)

	if err := op.DumpFull(ctx, dumpDir); err != nil {
		return fmt.Errorf("failed to take a full dump: %w", err)
	}

	gtid, err := bkop.GetGTIDExecuted(dumpDir)
	if err != nil {
		return fmt.Errorf("failed to get GTID set from the dump: %w", err)
	}
	bm.gtidSet = gtid

	usage, err := dirUsage(dumpDir)
	if err != nil {
		return fmt.Errorf("failed to calculate dir usage: %w", err)
	}
	bm.workDirUsage = usage
	bm.log.Info("work dir usage (full dump)", "bytes", usage)

	tarCmd := exec.Command("tar", "-c", "-f", "-", "-C", bm.workDir, "dump")
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	defer func() {
		if pr != nil {
			pr.Close()
		}
		if pw != nil {
			pw.Close()
		}
	}()
	tarCmd.Stdout = pw
	tarCmd.Stderr = os.Stderr

	if err := tarCmd.Start(); err != nil {
		return fmt.Errorf("failed to start tar process: %w", err)
	}
	pw.Close()
	pw = nil

	bw := &ByteCountWriter{}
	key := calcKey(bm.cluster.Namespace, bm.cluster.Name, constants.DumpFilename, bm.startTime)
	if err := bm.bucket.Put(ctx, key, io.TeeReader(pr, bw), usage); err != nil {
		return fmt.Errorf("failed to put dump.tar: %w", err)
	}
	if err := tarCmd.Wait(); err != nil {
		return fmt.Errorf("tar command failed: %w", err)
	}

	bm.dumpSize = bw.Written()
	bm.log.Info("uploaded dump file", "key", key, "bytes", bm.dumpSize)
	return nil
}

func (bm *BackupManager) backupBinlog(ctx context.Context, op bkop.Operator) error {
	binlogDir := filepath.Join(bm.workDir, "binlog")
	if err := os.MkdirAll(binlogDir, 0755); err != nil {
		return fmt.Errorf("failed to make binlog dump directory: %w", err)
	}
	defer os.RemoveAll(binlogDir)

	lastBackup := &bm.cluster.Status.Backup
	binlogName := lastBackup.BinlogFilename
	if bm.sourceIndex != lastBackup.SourceIndex {
		binlogs, err := op.GetBinlogs(ctx)
		if err != nil {
			return fmt.Errorf("failed to list binlog files: %w", err)
		}
		if len(binlogs) == 0 {
			return fmt.Errorf("no binlog files found")
		}
		bkop.SortBinlogs(binlogs)
		binlogName = binlogs[0]
	}

	if err := op.DumpBinlog(ctx, binlogDir, binlogName, lastBackup.GTIDSet); err != nil {
		return fmt.Errorf("failed to take a binlog backup: %w", err)
	}

	usage, err := dirUsage(binlogDir)
	if err != nil {
		return fmt.Errorf("failed to calculate dir usage: %w", err)
	}
	bm.log.Info("work dir usage (binlog)", "bytes", usage)
	if usage > bm.workDirUsage {
		bm.workDirUsage = usage
	}

	tarCmd := exec.Command("tar", "-c", "-f", "-", "-C", bm.workDir, "binlog")
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	defer func() {
		if pr != nil {
			pr.Close()
		}
		if pw != nil {
			pw.Close()
		}
	}()
	tarCmd.Stdout = pw
	tarCmd.Stderr = os.Stderr

	if err := tarCmd.Start(); err != nil {
		return fmt.Errorf("failed to start tar process: %w", err)
	}
	pw.Close()
	pw = nil

	zstdCmd := exec.Command("zstd", "--no-progress", "-T"+fmt.Sprint(bm.threads))
	zstdCmd.Stdin = pr
	pr2, pw2, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	defer func() {
		if pr2 != nil {
			pr2.Close()
		}
		if pw2 != nil {
			pw2.Close()
		}
	}()
	zstdCmd.Stdout = pw2
	zstdCmd.Stderr = os.Stderr

	if err := zstdCmd.Start(); err != nil {
		return fmt.Errorf("failed to start zstd process: %w", err)
	}
	pw2.Close()
	pw2 = nil

	bw := &ByteCountWriter{}
	key := calcKey(bm.cluster.Namespace, bm.cluster.Name, constants.BinlogFilename, lastBackup.Time.Time)
	if err := bm.bucket.Put(ctx, key, io.TeeReader(pr2, bw), usage); err != nil {
		return fmt.Errorf("failed to put binlog.tar.zst: %w", err)
	}
	if err := tarCmd.Wait(); err != nil {
		return fmt.Errorf("tar command failed: %w", err)
	}
	if err := zstdCmd.Wait(); err != nil {
		return fmt.Errorf("zstd command failed: %w", err)
	}

	bm.binlogSize = bw.Written()
	bm.log.Info("uploaded binlog files", "key", key, "bytes", bm.binlogSize)
	return nil
}

func podIsReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type != corev1.PodReady {
			continue
		}
		return cond.Status == corev1.ConditionTrue
	}
	return false
}

func dirUsage(dir string) (int64, error) {
	var usage int64
	fn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			usage += info.Size()
		} else {
			usage += st.Blocks * 512
		}
		return nil
	}
	if err := filepath.WalkDir(dir, fn); err != nil {
		return 0, err
	}

	return usage, nil
}
