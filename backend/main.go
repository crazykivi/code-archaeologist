package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"code-archaeologist/backend/internal/config"
	"code-archaeologist/backend/internal/handlers"
	"code-archaeologist/backend/internal/server"
	"code-archaeologist/backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[Config] failed to load config: %v", err)
	}

	log.Printf("[Config] env=%s addr=%s default_provider=%s db=%s",
		cfg.AppEnv, cfg.Addr, cfg.DefaultProvider, cfg.DBPath)

	var dataStore store.Store
	if cfg.DBPath == ":memory:" {
		log.Println("[Store] using in-memory store")
		dataStore = store.NewMemoryStore()
	} else {
		log.Printf("[Store] opening SQLite at %s", cfg.DBPath)
		sqliteStore, err := store.NewSQLiteStore(cfg.DBPath)
		if err != nil {
			log.Fatalf("[Store] failed to init SQLite: %v", err)
		}
		dataStore = sqliteStore
		defer dataStore.Close()
	}

	api := handlers.New(cfg, dataStore)
	router := server.New(cfg, api)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("[Server] listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[Server] listen error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Server] shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[Server] shutdown error: %v", err)
	}

	log.Println("[Server] stopped")
}
