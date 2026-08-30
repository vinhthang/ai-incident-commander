package main

import (
	"context"
	"log"
	"net/http"
	"os"
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
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	github.InitClient()
	workspace.InitWorkspace()

	http.HandleFunc("/webhook", webhook.HandleWebhook)
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}
	
	log.Printf("🚀 Modular AI Incident Commander starting on :%s (model: %s)", port, config.GeminiModel)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
