package e2e

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"text/template"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/makebucket.yaml
var makeBucketYAML string

//go:embed testdata/backup.yaml
var backupYAML string

//go:embed testdata/restore.yaml
var restoreYAML string

var _ = Context("backup", func() {
	if doUpgrade {
		return
	}

	var restorePoint time.Time

	It("should create a bucket", func() {
		kubectlSafe([]byte(makeBucketYAML), "apply", "-f", "-")
		Eventually(func(g Gomega) {
			out, err := kubectl(nil, "get", "jobs", "make-bucket", "-o", "json")
			g.Expect(err).NotTo(HaveOccurred())
			job := &batchv1.Job{}
			err = json.Unmarshal(out, job)
			g.Expect(err).NotTo(HaveOccurred())
			condComplete, err := getJobCondition(job, batchv1.JobComplete)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(condComplete.Status).To(Equal(corev1.ConditionTrue), "make-bucket has not been finished")
		}).Should(Succeed())
	})

	It("should construct a source cluster", func() {
		kubectlSafe(fillTemplate(backupYAML), "apply", "-f", "-")
		Eventually(func(g Gomega) {
			cluster, err := getCluster("backup", "source")
			g.Expect(err).NotTo(HaveOccurred())
			condHealthy, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(condHealthy.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())

		kubectlSafe(nil, "moco", "-n", "backup", "mysql", "-u", "moco-writable", "source", "--",
			"-e", "CREATE DATABASE test")
		kubectlSafe(nil, "moco", "-n", "backup", "mysql", "-u", "moco-writable", "source", "--",
			"-D", "test", "-e", "CREATE TABLE t (id INT NOT NULL AUTO_INCREMENT, data VARCHAR(32) NOT NULL, PRIMARY KEY (id), KEY key1 (data), KEY key2 (data, id)) ENGINE=InnoDB")
		kubectlSafe(nil, "moco", "-n", "backup", "mysql", "-u", "moco-writable", "source", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('aaa')")
	})

	It("should take a full dump", func() {
		kubectlSafe(nil, "-n", "backup", "create", "job", "--from=cronjob/moco-backup-source", "backup-1")
		Eventually(func(g Gomega) {
			out, err := kubectl(nil, "-n", "backup", "get", "jobs", "backup-1", "-o", "json")
			g.Expect(err).NotTo(HaveOccurred())
			job := &batchv1.Job{}
			err = json.Unmarshal(out, job)
			g.Expect(err).NotTo(HaveOccurred())
			condComplete, err := getJobCondition(job, batchv1.JobComplete)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(condComplete.Status).To(Equal(corev1.ConditionTrue), "backup-1 has not been finished")
		}).Should(Succeed())
	})

	It("should take an incremental backup", func() {
		kubectlSafe(nil, "moco", "-n", "backup", "mysql", "-u", "moco-writable", "source", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('bbb')")
		time.Sleep(1100 * time.Millisecond)
		restorePoint = time.Now().UTC()
		time.Sleep(1100 * time.Millisecond)
		kubectlSafe(nil, "moco", "-n", "backup", "mysql", "-u", "moco-admin", "source", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "FLUSH LOCAL BINARY LOGS")
		kubectlSafe(nil, "moco", "-n", "backup", "mysql", "-u", "moco-writable", "source", "--",
			"-D", "test", "--init_command=SET autocommit=1", "-e", "INSERT INTO t (data) VALUES ('ccc')")
		time.Sleep(100 * time.Millisecond)

		kubectlSafe(nil, "-n", "backup", "create", "job", "--from=cronjob/moco-backup-source", "backup-2")
		Eventually(func(g Gomega) {
			out, err := kubectl(nil, "-n", "backup", "get", "jobs", "backup-2", "-o", "json")
			g.Expect(err).NotTo(HaveOccurred())
			job := &batchv1.Job{}
			err = json.Unmarshal(out, job)
			g.Expect(err).NotTo(HaveOccurred())
			condComplete, err := getJobCondition(job, batchv1.JobComplete)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(condComplete.Status).To(Equal(corev1.ConditionTrue), "backup-2 has not been finished")
		}).Should(Succeed())

		cluster, err := getCluster("backup", "source")
		Expect(err).NotTo(HaveOccurred())
		Expect(cluster.Status.Backup.BinlogSize).NotTo(Equal(int64(0)))
	})

	It("should destroy the source then restore the backup data", func() {
		kubectlSafe(nil, "-n", "backup", "delete", "mysqlclusters", "source")

		tmpl, err := template.New("").Parse(restoreYAML)
		Expect(err).NotTo(HaveOccurred())
		buf := new(bytes.Buffer)
		err = tmpl.Execute(buf, struct {
			MySQLVersion string
			RestorePoint string
		}{
			mysqlVersion,
			restorePoint.Format(time.RFC3339),
		})
		Expect(err).NotTo(HaveOccurred())

		kubectlSafe(buf.Bytes(), "apply", "-f", "-")
		Eventually(func(g Gomega) {
			cluster, err := getCluster("backup", "target")
			g.Expect(err).NotTo(HaveOccurred())
			condHealthy, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(condHealthy.Status).To(Equal(metav1.ConditionTrue), "target is not healthy")
		}).Should(Succeed())

		out := kubectlSafe(nil, "moco", "-n", "backup", "mysql", "target", "--",
			"-N", "-D", "test", "-e", "SELECT COUNT(*) FROM t")
		count, err := strconv.Atoi(strings.TrimSpace(string(out)))
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(2))
	})

	It("should delete clusters", func() {
		kubectlSafe(nil, "delete", "-n", "backup", "mysqlclusters", "--all")

		Eventually(func(g Gomega) {
			out, err := kubectl(nil, "get", "-n", "backup", "pod", "-o", "json")
			g.Expect(err).NotTo(HaveOccurred())
			pods := &corev1.PodList{}
			err = json.Unmarshal(out, pods)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(pods.Items)).To(BeNumerically(">", 0), "wait until all Pods are deleted")
		}).Should(Succeed())
	})
})
