package minion

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"os/exec"

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

func executeAgy(job AgyJob) {
	_, span := tracer.Start(job.Ctx, "executeAgyQueueJob")
	defer span.End()

	log.Println("Executing agy CLI job from queue...")
	span.AddEvent("Starting isolated AGY process")

	cmd := exec.CommandContext(job.Ctx, "/usr/local/bin/agy", "-p", job.Prompt, "--dangerously-skip-permissions")
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
