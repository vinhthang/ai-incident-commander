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

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/webhook"
	"vinhthang.dev/ai-incident-commander/internal/workspace"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

func initTracer() *sdktrace.TracerProvider {
	ctx := context.Background()

	var exp sdktrace.SpanExporter
	var err error

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		exp, err = otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	} else {
		exp, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	}

	if err != nil {
		log.Fatalf("Failed to initialize tracing exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("ai-incident-commander"),
		),
	)
	if err != nil {
		log.Fatalf("Failed to create trace resource: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp
}

func main() {
	if config.GithubToken == "" || config.GeminiAPIKey == "" {
		log.Println("WARNING: GITHUB_TOKEN or GEMINI_API_KEY is not set. API calls will fail.")
	}

	tp := initTracer()

	github.InitClient()
	workspace.InitWorkspace()

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", webhook.HandleWebhook)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Listen for OS termination signals for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		log.Printf("🚀 Modular AI Incident Commander starting on :%s (model: %s)", port, config.GeminiModel)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Block until signal is received
	<-ctx.Done()
	log.Println("Shutting down AI Incident Commander gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down HTTP server: %v", err)
	}

	if err := tp.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down tracer provider: %v", err)
	}

	log.Println("AI Incident Commander stopped cleanly.")
}
