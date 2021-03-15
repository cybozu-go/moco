package e2e

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cybozu-go/moco"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func testKubectlMoco() {
	It("should run mysql", func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())

		fmt.Println("./bin/kubectl-moco", "-n", cluster.Namespace, "mysql", "-u", "moco-writabel", cluster.Name, "--", "--version")
		stdout, stderr, err := execAtLocal("./bin/kubectl-moco", []byte{}, "-n", cluster.Namespace, "mysql", "-u", "moco-writable", cluster.Name, "--", "--version")
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		Expect(string(stdout)).Should(ContainSubstring("mysql  Ver 8"))
	})

	It("should run mysql from stdin", func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())

		stdout, stderr, err := execAtLocal("./bin/kubectl-moco", []byte("select count(*) from moco_e2e.replication_test"), "-n", cluster.Namespace, "mysql", "-u", "moco-readonly", "-i", cluster.Name)
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		Expect(string(stdout)).Should(ContainSubstring(strconv.Itoa(lineCount)))
	})

	It("should fetch credential for moco-writable", func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())

		stdout, stderr, err := execAtLocal("./bin/kubectl-moco", []byte{}, "-n", cluster.Namespace, "credential", "-u", "moco-writable", cluster.Name)
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		secret, err := getPasswordSecret(cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(strings.TrimSpace(string(stdout))).Should(Equal(string(secret.Data[moco.WritablePasswordEnvName])))
	})

	It("should fetch credential for moco-writable formatted by my.conf", func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())

		stdout, stderr, err := execAtLocal("./bin/kubectl-moco", []byte{}, "-n", cluster.Namespace, "credential", "-u", "moco-writable", "--format", "myconf", cluster.Name)
		Expect(err).ShouldNot(HaveOccurred(), "stdout=%s, stderr=%s", stdout, stderr)

		secret, err := getPasswordSecret(cluster)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(string(stdout)).Should(Equal(fmt.Sprintf(`[client]
user=moco-writable
password="%s"
`, secret.Data[moco.WritablePasswordEnvName])))
	})
}
