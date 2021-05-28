package backup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
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

type RestoreManager struct {
	log          logr.Logger
	client       client.Client
	scheme       *runtime.Scheme
	namespace    string
	name         string
	password     string
	threads      int
	bucket       bucket.Bucket
	keyPrefix    string
	restorePoint time.Time
	workDir      string
}

func NewRestoreManager(cfg *rest.Config, bc bucket.Bucket, dir, srcNS, srcName, ns, name, password string, threads int, restorePoint time.Time) (*RestoreManager, error) {
	log := zap.New(zap.WriteTo(os.Stderr), zap.StacktraceLevel(zapcore.DPanicLevel))
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := mocov1beta1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	prefix := calcPrefix(srcNS, srcName)
	return &RestoreManager{
		log:          log,
		client:       k8sClient,
		scheme:       scheme,
		namespace:    ns,
		name:         name,
		password:     password,
		threads:      threads,
		bucket:       bc,
		keyPrefix:    prefix,
		restorePoint: restorePoint,
		workDir:      dir,
	}, nil
}

func (rm *RestoreManager) Restore(ctx context.Context) error {
	cluster := &mocov1beta1.MySQLCluster{}
	cluster.Namespace = rm.namespace
	cluster.Name = rm.name
	podName := cluster.PodName(0)

	rm.log.Info("waiting for a pod to become ready", "name", podName)
	var pod *corev1.Pod
	for i := 0; i < 600; i++ {
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}

		pod = &corev1.Pod{}
		if err := rm.client.Get(ctx, client.ObjectKey{Namespace: rm.namespace, Name: podName}, pod); err != nil {
			continue
		}

		if pod.Status.PodIP != "" {
			break
		}
	}

	op, err := newOperator(pod.Status.PodIP, constants.MySQLPort, constants.AdminUser, rm.password, rm.threads)
	if err != nil {
		return fmt.Errorf("failed to create an operator: %w", err)
	}
	defer op.Close()

	// ping the database until it becomes ready
	rm.log.Info("waiting for the mysqld to become ready", "name", podName)
	for i := 0; i < 600; i++ {
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}

		if err := op.Ping(); err != nil {
			continue
		}
		st := &bkop.ServerStatus{}
		if err := op.GetServerStatus(ctx, st); err != nil {
			continue
		}
		if !st.SuperReadOnly {
			continue
		}
		break
	}

	keys, err := rm.bucket.List(ctx, rm.keyPrefix)
	if err != nil {
		return fmt.Errorf("failed to list object keys: %w", err)
	}
	sort.Strings(keys)

	dumpKey, binlogKey, backupTime := rm.FindNearestDump(keys)
	if dumpKey == "" {
		return fmt.Errorf("no available backup")
	}

	rm.log.Info("restoring from a backup", "dump", dumpKey, "binlog", binlogKey)

	if err := op.PrepareRestore(ctx); err != nil {
		return fmt.Errorf("failed to prepare instance for restoration: %w", err)
	}

	if err := rm.loadDump(ctx, op, dumpKey); err != nil {
		return fmt.Errorf("failed to load dump: %w", err)
	}

	rm.log.Info("loaded dump successfully")

	if !backupTime.Equal(rm.restorePoint) && binlogKey != "" {
		if err := rm.applyBinlog(ctx, op, binlogKey); err != nil {
			return fmt.Errorf("failed to apply transactions: %w", err)
		}
		rm.log.Info("applied binlog successfully")
	}

	if err := op.FinishRestore(ctx); err != nil {
		return fmt.Errorf("failed to finalize the restoration: %w", err)
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster = &mocov1beta1.MySQLCluster{}
		if err := rm.client.Get(ctx, client.ObjectKey{Namespace: rm.namespace, Name: rm.name}, cluster); err != nil {
			return err
		}

		t := metav1.Now()
		cluster.Status.RestoredTime = &t
		return rm.client.Status().Update(ctx, cluster)
	})
	if err != nil {
		return fmt.Errorf("failed to update MySQLCluster status: %w", err)
	}

	ref, err := reference.GetReference(rm.scheme, cluster)
	if err != nil {
		return fmt.Errorf("failed to get reference for MySQLCluster: %w", err)
	}
	ev := event.Restored.ToEvent(ref)
	if err := rm.client.Create(ctx, ev); err != nil {
		rm.log.Error(err, "failed to create an event for restoration completion")
	}
	rm.log.Info("restoration finished successfully")

	return nil
}

func (rm *RestoreManager) FindNearestDump(keys []string) (string, string, time.Time) {
	var nearest time.Time
	var nearestDump, nearestBinlog string

	for _, key := range keys {
		if strings.HasSuffix(key, constants.BinlogFilename) {
			nearestBinlog = key
			continue
		}
		if !strings.HasSuffix(key, constants.DumpFilename) {
			continue
		}

		bkt, err := time.Parse(constants.BackupTimeFormat, path.Base(path.Dir(key)))
		if err != nil {
			rm.log.Error(err, "invalid object key", "key", key)
			continue
		}
		if bkt.After(rm.restorePoint) {
			break
		}

		nearestDump = key
		nearest = bkt
		if path.Dir(nearestDump) != path.Dir(nearestBinlog) {
			nearestBinlog = ""
		}
	}

	return nearestDump, nearestBinlog, nearest
}

func (rm *RestoreManager) loadDump(ctx context.Context, op bkop.Operator, key string) error {
	r, err := rm.bucket.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get object %s: %w", key, err)
	}
	defer r.Close()

	dumpDir := filepath.Join(rm.workDir, "dump")
	defer func() {
		os.RemoveAll(dumpDir)
	}()

	tarCmd := exec.CommandContext(ctx, "tar", "-C", rm.workDir, "-x", "-f", "-")
	tarCmd.Stdin = r
	tarCmd.Stdout = os.Stdout
	tarCmd.Stderr = os.Stderr
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("failed to untar dump file: %w", err)
	}

	return op.LoadDump(ctx, dumpDir)
}

func (rm *RestoreManager) applyBinlog(ctx context.Context, op bkop.Operator, key string) error {
	r, err := rm.bucket.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get object %s: %w", key, err)
	}
	defer r.Close()

	binlogDir := filepath.Join(rm.workDir, "binlog")
	defer func() {
		os.RemoveAll(binlogDir)
	}()

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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	zstdCmd := exec.CommandContext(ctx, "zstd", "-d", "--no-progress")
	zstdCmd.Stdin = r
	zstdCmd.Stdout = pw
	zstdCmd.Stderr = os.Stderr

	if err := zstdCmd.Start(); err != nil {
		return fmt.Errorf("failed to start zstd: %w", err)
	}
	pw.Close()
	pw = nil

	tarCmd := exec.CommandContext(ctx, "tar", "-C", rm.workDir, "-x", "-f", "-")
	tarCmd.Stdin = pr
	tarCmd.Stdout = os.Stdout
	tarCmd.Stderr = os.Stderr

	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("failed to run tar: %w", err)
	}
	if err := zstdCmd.Wait(); err != nil {
		return fmt.Errorf("zstd exited abnormally: %w", err)
	}

	return op.LoadBinlog(ctx, binlogDir, rm.restorePoint)
}
