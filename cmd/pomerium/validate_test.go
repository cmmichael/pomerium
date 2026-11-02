package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validateTestBase = `authenticate_service_url: https://auth.example.com
idp_provider: google
idp_client_id: x
idp_client_secret: y
insecure_server: true
address: :8080
`

const validateTestRoute = `routes:
  - from: https://a.example.com
    to: http://127.0.0.1:9000
    allow_any_authenticated_user: true
`

func TestValidateCommand(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	t.Setenv("SHARED_SECRET", secret)
	t.Setenv("COOKIE_SECRET", secret)

	badRegexRoute := validateTestRoute + "    regex_rewrite_pattern: \"(\"\n    regex_rewrite_substitution: x\n"

	for _, tc := range []struct {
		name    string
		config  string
		args    []string
		wantErr string
	}{
		{name: "valid", config: validateTestRoute},
		{name: "valid with envoy", config: validateTestRoute, args: []string{"--envoy"}},
		{name: "invalid yaml", config: "routes: [\n", wantErr: "failed to read config"},
		{name: "invalid route", config: "routes:\n  - from: not a url\n    to: http://127.0.0.1:9000\n", wantErr: "validation error"},
		{name: "envoy-only error is missed without --envoy", config: badRegexRoute},
		{name: "envoy-only error is caught with --envoy", config: badRegexRoute, args: []string{"--envoy"}, wantErr: "envoy rejected"},
		{name: "unknown key allowed by default", config: "routess: []\n"},
		{name: "unknown key fails with --strict", config: "routess: []\n", args: []string{"--strict"}, wantErr: "routess"},
		{name: "x- key allowed with --strict", config: "x-anchor: 1\n" + validateTestRoute, args: []string{"--strict"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(validateTestBase+tc.config), 0o600))

			cmd := buildValidateCommand(&path)
			cmd.SetArgs(tc.args)
			cmd.SilenceErrors = true
			var out bytes.Buffer
			cmd.SetOut(&out)

			err := cmd.ExecuteContext(t.Context())
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Contains(t, out.String(), "configuration is valid")
		})
	}
}
