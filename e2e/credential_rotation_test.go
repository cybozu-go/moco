package e2e

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed testdata/credential_rotation.yaml
var credentialRotationYAML string

func getCredentialRotation(ns, name string) (*mocov1beta2.CredentialRotation, error) {
	out, err := kubectl(nil, "get", "-n", ns, "credentialrotation", name, "-o", "json")
	if err != nil {
		return nil, err
	}
	cr := &mocov1beta2.CredentialRotation{}
	if err := json.Unmarshal(out, cr); err != nil {
		return nil, err
	}
	return cr, nil
}

func credentialRotationConditionIs(ns, name, condType string, status metav1.ConditionStatus) func() error {
	return func() error {
		cr, err := getCredentialRotation(ns, name)
		if err != nil {
			return err
		}
		for _, cond := range cr.Status.Conditions {
			if cond.Type != condType {
				continue
			}
			if cond.Status == status {
				return nil
			}
			return fmt.Errorf("condition %s is %s (reason=%s), not %s", condType, cond.Status, cond.Reason, status)
		}
		return fmt.Errorf("no %s condition", condType)
	}
}

func clusterHealthyIs(ns, name string, status metav1.ConditionStatus) func() error {
	return func() error {
		cluster, err := getCluster(ns, name)
		if err != nil {
			return err
		}
		cond, err := getClusterCondition(cluster, mocov1beta2.ConditionHealthy)
		if err != nil {
			return err
		}
		if cond.Status != status {
			return fmt.Errorf("cluster Healthy is %s, not %s", cond.Status, status)
		}
		return nil
	}
}

func rotationAdminPassword(ns, secretName string) string {
	out := kubectlSafe(nil, "get", "-n", ns, "secret", secretName, "-o", "json")
	secret := &corev1.Secret{}
	err := json.Unmarshal(out, secret)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	pass, ok := secret.Data["ADMIN_PASSWORD"]
	ExpectWithOffset(1, ok).To(BeTrue(), "secret %s/%s has no ADMIN_PASSWORD key", ns, secretName)
	return string(pass)
}

// rotationMySQLAuth tries to authenticate as moco-admin with the given
// password directly against a mysqld instance, bypassing the distributed
// Secrets. This is how the tests distinguish the primary and the retained
// secondary password during the dual-password window.
func rotationMySQLAuth(ns, pod, password string) error {
	_, err := kubectl(nil, "exec", "-n", ns, pod, "-c", "mysqld", "--",
		"mysql", "-u", "moco-admin", "-p"+password, "-N", "-e", "SELECT 1")
	return err
}

func rotationDualPasswordCount(ns, pod, password string) (string, error) {
	out, err := kubectl(nil, "exec", "-n", ns, pod, "-c", "mysqld", "--",
		"mysql", "-u", "moco-admin", "-p"+password, "-N", "-e",
		`SELECT COUNT(*) FROM mysql.user WHERE User LIKE 'moco-%' AND User_attributes->>'$.additional_password' IS NOT NULL`)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var _ = Context("credential rotation", Ordered, func() {
	if doUpgrade {
		return
	}

	const ns = "rotation"
	const controllerSecret = "mysql-rotation.test"

	BeforeAll(func() {
		GinkgoWriter.Println("construct a 3-instance cluster")
		kubectlSafe(fillTemplate(credentialRotationYAML), "apply", "-f", "-")
		Eventually(clusterHealthyIs(ns, "test", metav1.ConditionTrue)).Should(Succeed())

		DeferCleanup(func() {
			GinkgoWriter.Println("delete clusters")
			kubectlSafe(nil, "delete", "-n", ns, "credentialrotations", "--all")
			kubectlSafe(nil, "delete", "-n", ns, "mysqlclusters", "--all")
			verifyAllPodsDeleted(ns)
		})
	})

	It("should reject invalid CredentialRotation objects at admission", func() {
		By("creating a CR whose target MySQLCluster does not exist")
		_, err := kubectl([]byte(`
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata: {namespace: rotation, name: nosuch}
spec: {rotationGeneration: 1, discardGeneration: 0}
`), "apply", "-f", "-")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("MySQLCluster with the same name must exist"))

		By("creating a CR with rotationGeneration != 1")
		_, err = kubectl([]byte(`
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata: {namespace: rotation, name: test}
spec: {rotationGeneration: 2, discardGeneration: 0}
`), "apply", "-f", "-")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must be 1 at create time"))

		By("creating a CR with discardGeneration != 0")
		_, err = kubectl([]byte(`
apiVersion: moco.cybozu.com/v1beta2
kind: CredentialRotation
metadata: {namespace: rotation, name: test}
spec: {rotationGeneration: 1, discardGeneration: 1}
`), "apply", "-f", "-")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must be 0 at create time"))
	})

	It("should complete a full rotation and discard cycle", func() {
		oldPass := rotationAdminPassword(ns, "moco-test")

		By("starting a rotation")
		kubectlSafe(nil, "moco", "-n", ns, "rotate-credential", "test")

		By("refusing rotate / discard while the cycle is in flight")
		_, err := kubectl(nil, "moco", "-n", ns, "rotate-credential", "test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("in flight"))
		_, err = kubectl(nil, "moco", "-n", ns, "discard-old-credential", "test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("awaiting discard"))

		By("waiting for the verification window (DiscardReady=True)")
		Eventually(credentialRotationConditionIs(ns, "test", mocov1beta2.ConditionDiscardReady, metav1.ConditionTrue)).
			WithTimeout(20 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("checking the dual-password state")
		newPass := rotationAdminPassword(ns, "moco-test")
		Expect(newPass).NotTo(Equal(oldPass))
		// The controller Secret's current passwords match the distributed ones.
		Expect(rotationAdminPassword("moco-system", controllerSecret)).To(Equal(newPass))
		// Both passwords authenticate on every instance.
		for i := range 3 {
			pod := fmt.Sprintf("moco-test-%d", i)
			Expect(rotationMySQLAuth(ns, pod, oldPass)).To(Succeed(), "old password on %s", pod)
			Expect(rotationMySQLAuth(ns, pod, newPass)).To(Succeed(), "new password on %s", pod)
		}
		count, err := rotationDualPasswordCount(ns, "moco-test-0", newPass)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal("8"), "all 8 system users should hold a dual password")

		By("discarding the old passwords")
		// Right after the rolling restart the cluster may briefly be
		// un-Healthy, and the CLI refuses then; retry until it goes through.
		Eventually(func() error {
			_, err := kubectl(nil, "moco", "-n", ns, "discard-old-credential", "test")
			return err
		}).WithPolling(2 * time.Second).Should(Succeed())
		Eventually(credentialRotationConditionIs(ns, "test", mocov1beta2.ConditionRotationReady, metav1.ConditionTrue)).
			WithPolling(5 * time.Second).Should(Succeed())

		By("checking the post-discard state")
		Expect(rotationMySQLAuth(ns, "moco-test-0", oldPass)).NotTo(Succeed(), "old password must be discarded")
		Expect(rotationMySQLAuth(ns, "moco-test-0", newPass)).To(Succeed())
		count, err = rotationDualPasswordCount(ns, "moco-test-0", newPass)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal("0"), "no system user should hold a dual password")

		By("checking that the controller Secret has no bookkeeping keys left")
		out := kubectlSafe(nil, "get", "-n", "moco-system", "secret", controllerSecret, "-o", "json")
		secret := &corev1.Secret{}
		Expect(json.Unmarshal(out, secret)).To(Succeed())
		for key := range secret.Data {
			Expect(key).NotTo(HaveSuffix("_OLD"))
			Expect(key).NotTo(HaveSuffix("_PENDING"))
			Expect(key).NotTo(Equal("ROTATION_ID"))
			Expect(key).NotTo(Equal("RETAIN_STARTED"))
		}

		By("checking the observed generations")
		cr, err := getCredentialRotation(ns, "test")
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Status.ObservedRotationGeneration).To(Equal(cr.Spec.RotationGeneration))
		Expect(cr.Status.ObservedDiscardGeneration).To(Equal(cr.Spec.DiscardGeneration))
	})

	It("should pause while reconciliation is stopped and resume afterwards", func() {
		By("refusing rotate-credential while reconciliation is stopped")
		kubectlSafe(nil, "moco", "-n", ns, "stop", "reconciliation", "test")
		_, err := kubectl(nil, "moco", "-n", ns, "rotate-credential", "test")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciliation is stopped"))
		kubectlSafe(nil, "moco", "-n", ns, "start", "reconciliation", "test")

		By("starting a rotation and stopping reconciliation right after")
		Eventually(func() error {
			_, err := kubectl(nil, "moco", "-n", ns, "rotate-credential", "test")
			return err
		}).WithPolling(2 * time.Second).Should(Succeed())
		kubectlSafe(nil, "moco", "-n", ns, "stop", "reconciliation", "test")

		By("waiting for the RotationPaused warning Event")
		Eventually(func() error {
			out, err := kubectl(nil, "get", "-n", ns, "events",
				"--field-selector", "involvedObject.kind=CredentialRotation,involvedObject.name=test,reason=RotationPaused",
				"-o", "json")
			if err != nil {
				return err
			}
			events := &corev1.EventList{}
			if err := json.Unmarshal(out, events); err != nil {
				return err
			}
			if len(events.Items) == 0 {
				return errors.New("no RotationPaused event yet")
			}
			return nil
		}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("checking that the cluster keeps working while paused")
		currentPass := rotationAdminPassword("moco-system", controllerSecret)
		Expect(rotationMySQLAuth(ns, "moco-test-0", currentPass)).To(Succeed())

		By("resuming reconciliation and completing the cycle")
		kubectlSafe(nil, "moco", "-n", ns, "start", "reconciliation", "test")
		Eventually(credentialRotationConditionIs(ns, "test", mocov1beta2.ConditionDiscardReady, metav1.ConditionTrue)).
			WithTimeout(20 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		Eventually(func() error {
			_, err := kubectl(nil, "moco", "-n", ns, "discard-old-credential", "test")
			return err
		}).WithPolling(2 * time.Second).Should(Succeed())
		Eventually(credentialRotationConditionIs(ns, "test", mocov1beta2.ConditionRotationReady, metav1.ConditionTrue)).
			WithPolling(5 * time.Second).Should(Succeed())
	})

	// Runs after two completed cycles, so both generations are 2 and a
	// decrement to 1 passes the CRD schema (rotationGeneration >= 1) and
	// exercises the webhook's monotonicity check.
	It("should reject invalid generation updates at admission", func() {
		By("decreasing the generations from 2 to 1")
		_, err := kubectl(nil, "patch", "-n", ns, "credentialrotation", "test",
			"--type", "merge", "-p", `{"spec":{"rotationGeneration":1,"discardGeneration":1}}`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("monotonically increasing"))

		By("decreasing rotationGeneration below the schema minimum")
		_, err = kubectl(nil, "patch", "-n", ns, "credentialrotation", "test",
			"--type", "merge", "-p", `{"spec":{"rotationGeneration":0,"discardGeneration":0}}`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("greater than or equal to 1"))

		By("increasing rotationGeneration and discardGeneration together")
		_, err = kubectl(nil, "patch", "-n", ns, "credentialrotation", "test",
			"--type", "merge", "-p", `{"spec":{"rotationGeneration":3,"discardGeneration":3}}`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("can only increment discardGeneration"))
	})
})
