package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"
)

const (
	outboxWorkerIDEnv       = "PULP_OUTBOX_WORKER_ID"
	outboxPollIntervalEnv   = "PULP_OUTBOX_POLL_INTERVAL_SECONDS"
	outboxBatchLimitEnv     = "PULP_OUTBOX_BATCH_LIMIT"
	defaultOutboxPollPeriod = 5 * time.Second
	defaultOutboxBatchLimit = 25
)

type outboxServiceConfig struct {
	WorkerID string
	Interval time.Duration
	Limit    int
}

func outboxServiceConfigFromEnv() outboxServiceConfig {
	workerID := os.Getenv(outboxWorkerIDEnv)
	if workerID == "" {
		workerID, _ = os.Hostname()
	}
	if workerID == "" {
		workerID = "bananapulse-pulp-host"
	}
	interval := defaultOutboxPollPeriod
	if seconds, err := strconv.Atoi(os.Getenv(outboxPollIntervalEnv)); err == nil && seconds > 0 {
		interval = time.Duration(seconds) * time.Second
	}
	limit := defaultOutboxBatchLimit
	if parsed, err := strconv.Atoi(os.Getenv(outboxBatchLimitEnv)); err == nil && parsed > 0 {
		limit = parsed
	}
	return outboxServiceConfig{WorkerID: workerID, Interval: interval, Limit: limit}
}

type outboxService struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startOutboxService(client *applicationClient, sender emailSender, config outboxServiceConfig) (*outboxService, error) {
	worker, err := newEmailOutboxWorker(client, sender)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &outboxService{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(service.done)
		drain := func() {
			if _, err := worker.DrainOnce(ctx, config.WorkerID, config.Limit); err != nil && ctx.Err() == nil {
				// The detailed effect outcome is durably recorded by the owner. Do
				// not log recipient/body/token data at this privileged boundary.
				log.Printf("Bananapulse email outbox drain failed")
			}
		}
		drain()
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				drain()
			}
		}
	}()
	return service, nil
}

func (s *outboxService) shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
