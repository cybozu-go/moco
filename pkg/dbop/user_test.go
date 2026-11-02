package dbop

import (
	"context"
	"strings"
	"testing"

	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/cybozu-go/moco/pkg/password"
)

func TestAuthenticationPluginRegexp(t *testing.T) {
	valid := []string{
		"mysql_native_password",
		"caching_sha2_password",
		"sha256_password",
		"plugin123",
	}
	for _, plugin := range valid {
		if !authenticationPluginRegexp.MatchString(plugin) {
			t.Errorf("expected %q to be valid", plugin)
		}
	}

	invalid := []string{
		"",
		"mysql-native-password",
		"caching sha2 password",
		"caching_sha2_password; DROP USER moco-admin",
		"caching_sha2_password`",
		"plugin.name",
		strings.Repeat("a", 65),
	}
	for _, plugin := range invalid {
		if authenticationPluginRegexp.MatchString(plugin) {
			t.Errorf("expected %q to be invalid", plugin)
		}
	}
}

func TestSetSystemUserAuthenticationPluginRejectsInvalidPlugin(t *testing.T) {
	operator := &operator{}
	for _, plugin := range []string{"", "caching-sha2-password", "caching_sha2_password; DROP USER moco-admin"} {
		if _, err := operator.SetSystemUserAuthenticationPlugin(context.Background(), plugin); err == nil {
			t.Errorf("expected %q to be rejected", plugin)
		}
	}
}

func TestPasswordForUser(t *testing.T) {
	passwd, err := password.NewMySQLPassword()
	if err != nil {
		t.Fatalf("failed to generate passwords: %v", err)
	}
	operator := &operator{passwd: passwd}
	want := map[string]string{
		constants.AdminUser:       passwd.Admin(),
		constants.AgentUser:       passwd.Agent(),
		constants.ReplicationUser: passwd.Replicator(),
		constants.CloneDonorUser:  passwd.Donor(),
		constants.ExporterUser:    passwd.Exporter(),
		constants.BackupUser:      passwd.Backup(),
		constants.ReadOnlyUser:    passwd.ReadOnly(),
		constants.WritableUser:    passwd.Writable(),
	}

	for _, user := range constants.MocoUsers {
		got, ok := operator.passwordForUser(user)
		if !ok {
			t.Errorf("no password mapping for %s", user)
			continue
		}
		if got != want[user] {
			t.Errorf("%s: got an unexpected password", user)
		}
	}
	if _, ok := operator.passwordForUser("not-a-moco-user"); ok {
		t.Error("unexpected password mapping for an unknown user")
	}

	operator.passwd = nil
	if _, ok := operator.passwordForUser(constants.AdminUser); ok {
		t.Error("unexpected password mapping without a password set")
	}
}
