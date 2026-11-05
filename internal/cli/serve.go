// Package cli implements the wvf command surface (cobra).
package cli

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/scheduler"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/server"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var cfgPath string
	var tick time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the dashboard + scheduler daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, st, err := cfgAndStore(cfgPath)
			if err != nil {
				return err
			}
			defer st.Close()
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
			srv := &http.Server{Addr: cfg.Listen, Handler: h}
			go func() { <-ctx.Done(); srv.Shutdown(context.Background()) }()
			fmt.Printf("wvf listening on http://%s\n", cfg.Listen)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to wvf.yml")
	cmd.Flags().DurationVar(&tick, "tick", 60*time.Second, "scheduler sweep interval")
	return cmd
}

func cfgAndStore(cfgPath string) (config.Config, *store.Store, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return cfg, nil, err
	}
	st, err := store.Open(ctx0, cfg.StateDB)
	return cfg, st, err
}
