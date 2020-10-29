package e2e

import (
	"fmt"
	"os/exec"
	"syscall"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func testController() {
	It("should elect a leader instance of moco-controller", func() {
		_, _, err := kubectl("-n", "moco-system", "get", "configmaps", "moco")
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should expose metrics", func() {
		var listenPort = 18080
		var metricsPort = 8080
		portForwardCmd := exec.Command("./bin/kubectl", "-n", "moco-system", "port-forward", "deployment/moco-controller-manager", fmt.Sprintf("%d:%d", listenPort, metricsPort))
		portForwardCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		err := portForwardCmd.Start()
		Expect(err).ShouldNot(HaveOccurred())
		defer portForwardCmd.Process.Kill()
		Eventually(func() error {
			stdout, stderr, err := execAtLocal("curl", nil, "-sf", fmt.Sprintf("http://localhost:%d/metrics", listenPort))
			if err != nil {
				return fmt.Errorf("failed to curl. stdout: %s, stderr: %s, err: %v", stdout, stderr, err)
			}
			return nil
		}).Should(Succeed())
	})
}
