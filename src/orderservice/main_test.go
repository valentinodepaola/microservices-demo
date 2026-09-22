// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestRequiredEnv(t *testing.T) {
	t.Setenv("ORDERSERVICE_TEST_VAR", "")
	if _, err := requiredEnv("ORDERSERVICE_TEST_VAR"); err == nil {
		t.Fatal("expected an error for an empty variable, got nil")
	}

	t.Setenv("ORDERSERVICE_TEST_VAR", "redis-orders:6379")
	got, err := requiredEnv("ORDERSERVICE_TEST_VAR")
	if err != nil || got != "redis-orders:6379" {
		t.Fatalf("requiredEnv() = %q, %v; want %q, nil", got, err, "redis-orders:6379")
	}
}

// El readiness sigue a Redis: NOT_SERVING si no responde, SERVING cuando vuelve.
func TestWatchRedisFollowsRedis(t *testing.T) {
	var redisDown atomic.Bool
	redisDown.Store(true)
	ping := func(context.Context) error {
		if redisDown.Load() {
			return errors.New("connection refused")
		}
		return nil
	}

	hs := health.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchRedis(ctx, ping, hs, 10*time.Millisecond)

	waitForStatus(t, hs, healthpb.HealthCheckResponse_NOT_SERVING)
	redisDown.Store(false)
	waitForStatus(t, hs, healthpb.HealthCheckResponse_SERVING)
	redisDown.Store(true)
	waitForStatus(t, hs, healthpb.HealthCheckResponse_NOT_SERVING)
}

func waitForStatus(t *testing.T, hs *health.Server, want healthpb.HealthCheckResponse_ServingStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := hs.Check(context.Background(), &healthpb.HealthCheckRequest{Service: serviceName})
		if err == nil && resp.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("health status for %q never became %v", serviceName, want)
}
