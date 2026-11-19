package minion

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"vinhthang.dev/ai-incident-commander/internal/config"
)

type AgyJob struct {
	Ctx    context.Context
	Prompt string
	Result chan AgyResult
}

type AgyResult struct {
	Stdout string
	Stderr string
	Err    error
}

// AgyQueue holds pending AGY executions (buffered up to 100).
var AgyQueue = make(chan AgyJob, 100)

// StartAgyWorker launches the single background worker that processes AGY executions.
// This guarantees that only one AGY process runs at a time across the entire application.
func StartAgyWorker() {
	go func() {
		log.Println("🚀 Starting background AGY Worker Queue (Concurrency: 1)")
		for job := range AgyQueue {
			executeAgy(job)
		}
	}()
}

var agyRuntimeAuthFiles = []string{
	"antigravity-oauth-token",
	"installation_id",
	"settings.json",
}

func buildAgyArgs(prompt, model string) []string {
	args := []string{"-p", prompt, "--dangerously-skip-permissions", "--disable-slash-commands"}
	if trimmedModel := strings.TrimSpace(model); trimmedModel != "" {
		args = append(args, "--model", trimmedModel)
	}
	return args
}

func syncAgyRuntimeDir(hostAuthDir, runtimeGeminiDir string) error {
	if runtimeGeminiDir == "" {
		return nil
	}

	cliDir := filepath.Join(runtimeGeminiDir, "antigravity-cli")
	cfgDir := filepath.Join(runtimeGeminiDir, "config")
	if err := os.MkdirAll(cliDir, 0700); err != nil {
		return fmt.Errorf("create runtime antigravity-cli dir: %w", err)
	}
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		return fmt.Errorf("create runtime config dir: %w", err)
	}

	if hostAuthDir != "" {
		for _, name := range agyRuntimeAuthFiles {
			src := filepath.Join(hostAuthDir, name)
			data, err := os.ReadFile(src)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return fmt.Errorf("read host auth file %s: %w", name, err)
			}
			dst := filepath.Join(cliDir, name)
			if err := os.WriteFile(dst, data, 0600); err != nil {
				return fmt.Errorf("write runtime auth file %s: %w", name, err)
			}
		}
	}

	mcpPath := filepath.Join(cfgDir, "mcp_config.json")
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		return fmt.Errorf("write empty mcp_config.json: %w", err)
	}
	return nil
}

func executeAgy(job AgyJob) {
	_, span := tracer.Start(job.Ctx, "executeAgyQueueJob")
	defer span.End()

	if config.AgyHostAuthDir != "" {
		if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
			if syncErr := syncAgyRuntimeDir(config.AgyHostAuthDir, filepath.Join(homeDir, ".gemini")); syncErr != nil {
				log.Printf("⚠️ Failed to sync AGY runtime auth files: %v", syncErr)
			}
		}
	}

	log.Println("Executing agy CLI job from queue...")
	span.AddEvent("Starting isolated AGY process")

	args := buildAgyArgs(job.Prompt, config.AgyModel)
	cmd := exec.CommandContext(job.Ctx, "/usr/local/bin/agy", args...)
	cmd.Dir = config.WorkspaceDir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)

	err := cmd.Run()

	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
	}

	span.AddEvent("AGY process completed")

	// Send result back to the waiting goroutine
	job.Result <- AgyResult{
		Stdout: outBuf.String(),
		Stderr: errBuf.String(),
		Err:    err,
	}
}

// EnqueueAgyJob is a helper method used by minion functions to submit a prompt to the worker queue
// and block until the execution is finished.
func EnqueueAgyJob(ctx context.Context, prompt string) AgyResult {
	resultChan := make(chan AgyResult, 1)
	job := AgyJob{
		Ctx:    ctx,
		Prompt: prompt,
		Result: resultChan,
	}

	// Push to global channel
	AgyQueue <- job

	// Wait for the worker to finish and return
	return <-resultChan
}
