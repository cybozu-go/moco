package e2e

import (
	"bytes"
	"os/exec"
)

func execAtLocal(cmd string, input []byte, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	command := exec.Command(cmd, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr

	if len(input) != 0 {
		command.Stdin = bytes.NewReader(input)
	}

	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func kubectl(args ...string) ([]byte, []byte, error) {
	return execAtLocal("./bin/kubectl", nil, args...)
}

//lint:ignore U1000 This func may be used in the future.
func kubectlWithInput(input []byte, args ...string) ([]byte, []byte, error) {
	return execAtLocal("./bin/kubectl", input, args...)
}

//lint:ignore U1000 This func may be used in the future.
func containString(s []string, target string) bool {
	for _, ss := range s {
		if ss == target {
			return true
		}
	}
	return false
}
