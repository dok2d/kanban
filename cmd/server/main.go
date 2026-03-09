package main

import (
	"flag"
	"kanban/internal/db"
	"kanban/internal/handler"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"context"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("db", "/data/kanban.db", "sqlite database path")
	verbose := flag.Bool("verbose", false, "enable verbose logging")
	flag.Parse()

	store, err := db.New(*dbPath)
	if err != nil {
		log.Fatalf("db init: %v", err)
	}
	defer store.Close()

	h := handler.New(store)
	if *verbose {
		h.SetVerbose(true)
		log.Println("verbose logging enabled")
	}

	srv := &http.Server{
		Addr:         *addr,
		Handler:      h,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Clean expired sessions periodically
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			store.CleanExpiredSessions()
		}
	}()

	go func() {
		log.Printf("kanban listening on %s", *addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
