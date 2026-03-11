package dbop

import (
	"testing"
)

func TestParseAuthPlugin(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		want    string
		wantErr bool
	}{
		{
			name:   "default three-factor policy",
			policy: "*,,",
			want:   defaultAuthPlugin,
		},
		{
			name:   "explicit caching_sha2_password",
			policy: "caching_sha2_password,,",
			want:   "caching_sha2_password",
		},
		{
			name:   "mysql_native_password",
			policy: "mysql_native_password,,",
			want:   "mysql_native_password",
		},
		{
			name:   "wildcard only",
			policy: "*",
			want:   defaultAuthPlugin,
		},
		{
			name:   "empty string",
			policy: "",
			want:   defaultAuthPlugin,
		},
		{
			name:   "wildcard with second and third factors",
			policy: "*,auth_socket,",
			want:   defaultAuthPlugin,
		},
		{
			name:   "explicit plugin with other factors",
			policy: "caching_sha2_password,auth_socket,*",
			want:   "caching_sha2_password",
		},
		{
			name:   "leading space",
			policy: " caching_sha2_password,,",
			want:   "caching_sha2_password",
		},
		{
			name:   "trailing space",
			policy: "caching_sha2_password ,,",
			want:   "caching_sha2_password",
		},
		{
			name:    "invalid plugin name with special chars",
			policy:  "bad-plugin,,",
			wantErr: true,
		},
		{
			name:    "SQL injection attempt",
			policy:  "'; DROP TABLE mysql.user; --,,",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAuthPlugin(tt.policy)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseAuthPlugin(%q) returned nil error, want error", tt.policy)
				}
				return
			}
			if err != nil {
				t.Errorf("parseAuthPlugin(%q) returned error: %v", tt.policy, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseAuthPlugin(%q) = %q, want %q", tt.policy, got, tt.want)
			}
		})
	}
}
