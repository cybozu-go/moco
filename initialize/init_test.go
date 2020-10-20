package initialize

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testInitializeInstance(t *testing.T) {
	err := initializeInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func testShutdownInstance(t *testing.T) {
}

func testTouchInitOnceCompleted(t *testing.T) {
}

func testWaitInstanceBootstrap(t *testing.T) {
}

func TestInit(t *testing.T) {
	_, err := os.Stat(filepath.Join("/", ".dockerenv"))
	if err != nil {
		t.Skip("These tests should be run on docker")
	}
	t.Run("initializeInstance", testInitializeInstance)
	t.Run("shutdownInstance", testShutdownInstance)
	t.Run("touchInitOnceCompleted", testTouchInitOnceCompleted)
	t.Run("waitInstanceBootstrap", testWaitInstanceBootstrap)
}
