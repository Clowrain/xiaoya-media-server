package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/embyserver"
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

	if err := embyserver.Run(ctx, cfg, nil, nil); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}
