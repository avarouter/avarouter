package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/aggregateway/agw"
	"github.com/spf13/cobra"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	defaultAddr, invalidPort := agw.DefaultListenAddress()
	opts := agw.Options{ConfigPath: "config.yaml", Listen: defaultAddr}
	root := newRootCommand(&opts, defaultAddr, invalidPort, logger)
	if err := root.Execute(); err != nil {
		logger.Error("fatal", "error", err.Error())
		os.Exit(1)
	}
}

func newRootCommand(opts *agw.Options, defaultAddr, invalidPort string, logger *slog.Logger) *cobra.Command {
	root := &cobra.Command{
		Use:   "agw",
		Short: "Reverse proxy gateway for AI APIs",
		Long: "agw routes API requests to configured upstreams using routing\n" +
			"selectors, rewrites JSON bodies, and exposes a management UI for\n" +
			"configuration, the live request feed and the session journal.\n\n" +
			"Management endpoints (/, /config*, /logs, /sessions*) can be protected\n" +
			"with --admin-user/--admin-password or the AGW_ADMIN_USER and\n" +
			"AGW_ADMIN_PASSWORD environment variables.",
		Example: "  agw --config config.yaml --listen :8080\n" +
			"  AGW_ADMIN_USER=admin AGW_ADMIN_PASSWORD=secret agw",
		RunE: func(cmd *cobra.Command, args []string) error {
			if invalidPort != "" {
				logger.Error("server config error", "port", invalidPort, "fallback", defaultAddr)
			}
			return agw.RunWithOptions(*opts)
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	flags := root.Flags()
	flags.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to the config file")
	flags.StringVar(&opts.Listen, "listen", defaultAddr, "listen address ($PORT wins, else :8080)")
	flags.DurationVar(&opts.Timeout, "timeout", 0, "per-upstream request timeout; 0 disables it")
	flags.BoolVar(&opts.AllowDebug, "allow-debug", false, "honor debug: true from the client config and log request headers; without it debug stays off")
	flags.BoolVar(&opts.LogStderr, "log-stderr", false, "also write logs to stderr (default: only the /logs feed)")
	flags.BoolVar(&opts.Dev, "dev", false, "dev mode: load templates from disk with hot reload")
	flags.StringVar(&opts.DataDir, "data-dir", "", "persist sessions, payloads and logs to this directory")
	flags.StringVar(&opts.AdminUser, "admin-user", "", "Basic Auth username for the management UI (env: AGW_ADMIN_USER; must be paired with --admin-password)")
	flags.StringVar(&opts.AdminPassword, "admin-password", "", "Basic Auth password for the management UI (env: AGW_ADMIN_PASSWORD)")
	// Prepaid payment flags (x402 / Avalanche USDC). Multi-tenant:
	// there is no single owner; users self-register on first topup.
	// If --usdc-rpc / --usdc-address are set, payments are enabled.
	flags.StringVar(&opts.USDCAddress, "usdc-address", "", "USDC contract address (default: Fuji testnet 0x5425890298aed601595a70AB815c96711a31Bc65)")
	flags.StringVar(&opts.USDCRPCURL, "usdc-rpc", "", "EVM RPC URL for settlement (default: https://api.avax-test.network/ext/bc/C/rpc)")
	flags.Int64Var(&opts.USDCChainID, "usdc-chain-id", 0, "EVM chain id (default: 43113 Avalanche Fuji testnet)")
	flags.StringVar(&opts.ServerKeyPath, "usdc-server-key", "", "path to hex ECDSA private key for submitting transferWithAuthorization; empty = offline mode (verify signature only)")
	// Keep accepting single-dash long flags (-config) as the original CLI did;
	// pflag would otherwise read them as shorthand clusters.
	root.SetArgs(normalizeArgs(os.Args[1:]))
	return root
}

// normalizeArgs rewrites known long flags written with a single dash into
// double-dash form so Go-style invocations like -config keep working.
func normalizeArgs(args []string) []string {
	longFlags := map[string]bool{"config": true, "listen": true, "timeout": true, "allow-debug": true, "log-stderr": true, "admin-user": true, "admin-password": true, "dev": true, "data-dir": true, "pay-to": true, "usdc-address": true, "usdc-rpc": true, "usdc-chain-id": true, "usdc-server-key": true}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			name := strings.TrimPrefix(strings.SplitN(arg, "=", 2)[0], "-")
			if longFlags[name] {
				arg = "-" + arg
			}
		}
		out = append(out, arg)
	}
	return out
}
