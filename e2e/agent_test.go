package e2e

import (
	"fmt"
	"os/exec"
	"syscall"

	"github.com/cybozu-go/moco"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func testAgent() {
	var portForwardCmd *exec.Cmd
	var listenPort = 19080

	BeforeEach(func() {
		cluster, err := getMySQLCluster()
		Expect(err).ShouldNot(HaveOccurred())
		podName := fmt.Sprintf("%s-%d", moco.UniqueName(cluster), 0)
		portForwardCmd = exec.Command("./bin/kubectl", "-n", "e2e-test", "port-forward", "pod/"+podName, fmt.Sprintf("%d:%d", listenPort, moco.AgentPort))
		portForwardCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		err = portForwardCmd.Start()
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		if portForwardCmd != nil {
			portForwardCmd.Process.Kill()
		}
	})

	It("should expose metrics", func() {
		Eventually(func() error {
			stdout, stderr, err := execAtLocal("curl", nil, "-sf", fmt.Sprintf("http://localhost:%d/metrics", listenPort))
			if err != nil {
				return fmt.Errorf("failed to curl. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			return nil
		}).Should(Succeed())
	})
}
