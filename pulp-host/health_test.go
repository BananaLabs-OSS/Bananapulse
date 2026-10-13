package main

import (
	"context"
	"errors"
	"github.com/BananaLabs-OSS/Pulp/run"
	"github.com/vmihailenco/msgpack/v5"
	"net/http/httptest"
	"testing"
)

type readinessAccess struct {
	run.ApplicationProviderAccess
	err error
}

func (a readinessAccess) CallProvider(_ context.Context, cell, provider string, _ []byte) ([]byte, error) {
	if cell != "pulp-monitor" || provider != "monitor.v1.catalog" {
		return nil, errors.New("wrong readiness provider")
	}
	if a.err != nil {
		return nil, a.err
	}
	return msgpack.Marshal(map[string]any{"version": "monitor.v1"})
}
func TestHealthDependsOnMonitorProvider(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {errors.New("cell not running"), 503}} {
		b := &httpBridge{client: &applicationClient{access: readinessAccess{err: tc.err}}}
		response := httptest.NewRecorder()
		b.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/healthz", nil))
		if response.Code != tc.status {
			t.Fatalf("health status = %d; want %d", response.Code, tc.status)
		}
	}
}
