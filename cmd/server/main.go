package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"xiaoya-media-server/internal/adminapi"
	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/embyserver"
	"xiaoya-media-server/internal/integration"
	"xiaoya-media-server/internal/resource/alist"
	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/scheduler"
)

func main() {
	configPath := flag.String("config", "", "path to YAML configuration file")
	prepareSchema := flag.Bool("prepare-schema", false, "initialize/migrate PostgreSQL schema and exit")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if *prepareSchema || os.Getenv("SCHEMA_ONLY") == "1" {
		if err := embyserver.PrepareSchema(cfg, nil); err != nil {
			log.Fatalf("prepare schema: %v", err)
		}
		log.Printf("database schema prepared successfully")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var pipe *integration.Pipeline
	defer func() {
		if pipe != nil {
			_ = pipe.Close()
		}
	}()

	setup := func(app *embyserver.App) error {
		store := discovery.NewSQLStore(app.SQLDB(), "postgres")
		var pErr error
		pipe, pErr = integration.NewPipeline(ctx, cfg, app, store)
		if pErr != nil {
			return pErr
		}

		alistCli := alist.NewClient(alist.Options{
			BaseURL:  cfg.AList.BaseURL,
			Username: cfg.AList.Username,
			Password: cfg.AList.Password,
			Token:    cfg.AList.Token,
		})

		imp := alist.NewImporter(alistCli, pipe.Discovery(), pipe.STRMEngine(), func(paths []string) {
			seen := make(map[string]bool, len(paths))
			for _, p := range paths {
				dir := filepath.Clean(filepath.Dir(p))
				if dir != "" && dir != "." && !seen[dir] {
					seen[dir] = true
					app.TriggerScopedPathScan(dir)
				}
			}
		})

		sched := scheduler.New(store, pipe, imp, cfg.Monitor.Interval, cfg.Monitor.Enabled)
		sched.Start(ctx)

		adminHandler := adminapi.NewHandler(cfg, store, pipe, imp, sched)
		app.SetExtensionHandler(adminHandler.ServeExtension)
		return nil
	}

	if err := embyserver.Run(ctx, cfg, setup, nil); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}
