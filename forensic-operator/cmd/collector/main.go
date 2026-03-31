// cmd/collector/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/nxtcybernet/checkpointctl/internal/collector"
)

var (
	storagePath string
	authToken   string
	port        int
)

func init() {
	flag.StringVar(&storagePath, "storage-path", "/var/lib/forensics", "Path where forensic bundles will be stored")
	flag.StringVar(&authToken, "auth-token", "", "Pre-shared authentication token")
	flag.IntVar(&port, "port", 8080, "HTTP server port")
}

func main() {
	flag.Parse()

	// Setup structured logging
	log.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := log.Log.WithName("collector")

	if authToken == "" {
		authToken = os.Getenv("FORENSICS_AUTH_TOKEN")
		if authToken == "" {
			logger.Error(nil, "Authentication token is required. Use --auth-token or FORENSICS_AUTH_TOKEN env var")
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(storagePath, 0755); err != nil {
		logger.Error(err, "Failed to create storage directory", "path", storagePath)
		os.Exit(1)
	}

	logger.Info("Starting checkpoint-collector",
		"storagePath", storagePath,
		"port", port)

	// Create collector handler
	handler := collector.NewHandler(storagePath, authToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/trigger", handler.HandleTrigger)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(err, "HTTP server failed")
			os.Exit(1)
		}
	}()

	logger.Info("Collector is ready and listening", "address", server.Addr)

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down collector...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error(err, "Forced shutdown")
	}

	logger.Info("Collector stopped cleanly")
}
