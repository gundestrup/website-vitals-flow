// Package cli implements the wvf command surface (cobra).
package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/jobs"
	"github.com/gundestrup/website-vitals-flow/internal/scheduler"
	"github.com/gundestrup/website-vitals-flow/internal/server"
	"github.com/gundestrup/website-vitals-flow/internal/store"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var cfgPath string
	var tick time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the dashboard + scheduler daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, st, err := cfgAndStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			if n, err := st.FailStaleJobs(cmd.Context()); err == nil && n > 0 {
				fmt.Printf("marked %d stale jobs failed\n", n)
			}
			ctx, cancel := signal.NotifyContext(context.Background(),
				syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			runner := jobs.New(st, cfg)
			go runner.Start(ctx)
			go scheduler.Run(ctx, st, runner, tick)
			h := server.New(st, cfg, runner)
			replaceStaleServe(cfg.Listen)
			srv := &http.Server{Addr: cfg.Listen, Handler: h,
				ReadHeaderTimeout: 10 * time.Second}
			go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
			fmt.Printf("wvf listening on http://%s\n", cfg.Listen)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", cfgFlagHelp)
	cmd.Flags().DurationVar(&tick, "tick", 60*time.Second, "scheduler sweep interval")

	return cmd
}

// firstBin returns the first existing absolute path — fixed locations
// only, no PATH lookup (S4036).
func firstBin(paths ...string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// replaceStaleServe terminates a previous `wvf serve` still bound to the
// same address — the lingering-process case seen after rebuilds. A
// non-wvf listener is left alone; ListenAndServe will error normally.
func replaceStaleServe(addr string) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return
	}
	lsof := firstBin("/usr/sbin/lsof", "/usr/bin/lsof", "/bin/lsof")
	ps := firstBin("/bin/ps", "/usr/bin/ps")
	if lsof == "" || ps == "" {
		return
	}
	out, err := exec.Command(lsof, "-ti", "tcp:"+port, "-sTCP:LISTEN").Output() // #nosec G204 nosemgrep -- lsof from firstBin fixed paths, numeric port
	if err != nil || len(out) == 0 {
		return
	}
	for _, fs := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(fs)
		if err != nil {
			continue
		}
		args, _ := exec.Command(ps, "-p", fs, "-o", "command=").Output() // #nosec G204 nosemgrep -- ps from firstBin fixed paths, numeric pid
		if !strings.Contains(string(args), "wvf serve") {
			continue
		}
		fmt.Printf("replacing previous wvf serve (pid %d) on %s\n", pid, addr)
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
			time.Sleep(300 * time.Millisecond)
		}
	}
}

func cfgAndStore(cfgPath string) (config.Config, *store.Store, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return cfg, nil, err
	}
	st, err := store.Open(ctx0, cfg.StateDB)
	return cfg, st, err
}
