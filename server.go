// Command server runs the Mustard customer-data REST API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mustard-money/customer-data/assistant"
	"github.com/mustard-money/customer-data/auth"
	"github.com/mustard-money/customer-data/config"
	"github.com/mustard-money/customer-data/db"
	"github.com/mustard-money/customer-data/llm/lmstudio"
	"github.com/mustard-money/customer-data/llm/openai"
	"github.com/mustard-money/customer-data/server"
	"github.com/mustard-money/customer-data/taxdb"
	"github.com/mustard-money/customer-data/taxtables"
	"github.com/mustard-money/customer-data/tracing"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
			return fmt.Errorf("%w (DATABASE_URL isn't set, so the built-in default was used)", err)
		}
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, logger); err != nil {
		return err
	}
	taxData, err := taxtables.Embedded()
	if err != nil {
		return err
	}
	if err := taxdb.Load(ctx, pool, taxData, logger); err != nil {
		return err
	}

	var providers []*auth.Provider
	if len(cfg.AppleClientIDs) > 0 {
		providers = append(providers, auth.NewApple(context.Background(), cfg.AppleClientIDs))
	}
	if len(cfg.GoogleClientIDs) > 0 {
		providers = append(providers, auth.NewGoogle(context.Background(), cfg.GoogleClientIDs))
	}

	// Spans go to the OTLP endpoint in OTEL_EXPORTER_OTLP_ENDPOINT (e.g. Jaeger), if set.
	shutdownTracing, err := tracing.Setup(ctx)
	if err != nil {
		return err
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(flushCtx); err != nil {
			logger.Warn("tracing: flushing spans failed", "err", err)
		}
	}()

	var chat *assistant.Assistant
	assistantOpts := []assistant.Option{
		assistant.WithModelInfo(cfg.LLMProvider, cfg.LLMModel),
		assistant.WithContentTracing(cfg.TraceAssistantContent),
	}
	switch cfg.LLMProvider {
	case "openai":
		chat = assistant.New(openai.New(openai.Config{
			BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel,
			HTTPClient: &http.Client{Timeout: assistant.Timeout},
		}), assistantOpts...)
	case "lmstudio":
		chat = assistant.New(lmstudio.New(lmstudio.Config{
			BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel,
			HTTPClient: &http.Client{Timeout: assistant.Timeout},
		}), assistantOpts...)
	}

	srv := &http.Server{
		Addr: cfg.Addr(),
		Handler: server.New(pool, server.Options{
			CORSAllowedOrigins: cfg.CORSAllowedOrigins,
			Logger:             logger,
			AuthProviders:      providers,
			Assistant:          chat,
			TaxTables:          taxtables.NewTables(taxData),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr, "cors_allowed_origins", cfg.CORSAllowedOrigins,
			"apple_sign_in", len(cfg.AppleClientIDs) > 0, "google_sign_in", len(cfg.GoogleClientIDs) > 0,
			"assistant_llm", cfg.LLMProvider, "tracing", tracing.Enabled(),
			"trace_assistant_content", cfg.TraceAssistantContent)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("stopped")
	return nil
}
