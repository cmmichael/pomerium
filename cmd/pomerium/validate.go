package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/pomerium/pomerium/config"
	"github.com/pomerium/pomerium/config/envoyconfig"
	"github.com/pomerium/pomerium/config/envoyconfig/filemgr"
	"github.com/pomerium/pomerium/pkg/envoy"
	"github.com/pomerium/pomerium/pkg/envoy/files"
	"github.com/pomerium/pomerium/pkg/netutil"
)

const envoyValidateTimeout = time.Minute

func buildValidateCommand(configFile *string) *cobra.Command {
	var withEnvoy, strict bool
	cmd := &cobra.Command{
		Use:          "validate",
		Short:        "Check a configuration file without starting Pomerium",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *configFile == "" {
				return errors.New("validate: --config is required")
			}
			options, err := config.NewOptionsFromFile(*configFile)
			if err != nil {
				return err
			}
			if strict {
				unknown, err := config.UnknownKeysFromFile(*configFile)
				if err != nil {
					return err
				}
				if len(unknown) > 0 {
					return fmt.Errorf("validate: unknown config options: %s", strings.Join(unknown, ", "))
				}
			}
			if withEnvoy {
				if err := validateEnvoyConfig(cmd.Context(), options); err != nil {
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "configuration is valid")
			return nil
		},
	}
	cmd.Flags().BoolVar(&withEnvoy, "envoy", false, "also check the generated config with envoy --mode validate")
	cmd.Flags().BoolVar(&strict, "strict", false, "fail on unknown config options, except keys starting with x-")
	return cmd
}

// validateEnvoyConfig builds the fully static bootstrap for the options and asks
// the embedded Envoy to validate it. Nothing binds a port and no upstream is contacted.
func validateEnvoyConfig(ctx context.Context, options *config.Options) error {
	ports, err := netutil.AllocatePorts(7)
	if err != nil {
		return fmt.Errorf("validate: allocating ports: %w", err)
	}
	cfg := config.New(options)
	cfg.EnvoyVersion = files.Lockfile().Version
	cfg.AllocatePorts(*(*[7]string)(ports))

	dir, err := os.MkdirTemp("", "pomerium-validate")
	if err != nil {
		return fmt.Errorf("validate: creating temporary directory: %w", err)
	}
	defer os.RemoveAll(dir)

	addr := func(port string) string { return "127.0.0.1:" + port }
	builder := envoyconfig.New(
		addr(cfg.ConnectPort),
		addr(cfg.GRPCPort),
		addr(cfg.HTTPPort),
		addr(cfg.DebugPort),
		addr(cfg.MetricsPort),
		filemgr.NewManager(filemgr.WithCacheDir(dir)),
		nil,
		false,
	)
	bootstrap, err := builder.BuildBootstrap(ctx, cfg, true, nil)
	if err != nil {
		return fmt.Errorf("validate: building envoy config: %w", err)
	}
	data, err := protojson.Marshal(bootstrap)
	if err != nil {
		return fmt.Errorf("validate: encoding envoy config: %w", err)
	}
	path := filepath.Join(dir, "envoy-config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("validate: writing envoy config: %w", err)
	}

	envoyPath, err := envoy.Extract()
	if err != nil {
		return fmt.Errorf("validate: extracting envoy: %w", err)
	}
	defer os.RemoveAll(filepath.Dir(envoyPath))

	ctx, cancel := context.WithTimeout(ctx, envoyValidateTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, envoyPath, "--mode", "validate", "-c", path, "--log-level", "error").CombinedOutput()
	if err != nil {
		return fmt.Errorf("validate: envoy rejected the generated config: %w\n%s", err, out)
	}
	return nil
}
