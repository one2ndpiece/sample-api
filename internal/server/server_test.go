package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPublicEndpoints(t *testing.T) {
	t.Parallel()

	app := newTestServer()

	tests := []struct {
		name   string
		path   string
		status int
	}{
		{name: "root", path: "/", status: http.StatusOK},
		{name: "health", path: "/healthz", status: http.StatusOK},
		{name: "ready", path: "/readyz", status: http.StatusOK},
		{name: "slow", path: "/slow", status: http.StatusOK},
		{name: "cpu", path: "/cpu", status: http.StatusOK},
		{name: "error", path: "/error", status: http.StatusInternalServerError},
		{name: "not found", path: "/missing", status: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			app.PublicHandler().ServeHTTP(recorder, request)

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, tt.status, recorder.Body.String())
			}
		})
	}
}

func TestReadyEndpointReflectsReadiness(t *testing.T) {
	t.Parallel()

	app := newTestServer()
	app.SetReady(false)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	app.PublicHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()

	app := newTestServer()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	app.PublicHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if allow := recorder.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow = %q, want GET, HEAD", allow)
	}
}

func TestMetricsEndpointIncludesApplicationMetrics(t *testing.T) {
	t.Parallel()

	app := newTestServer()
	app.PublicHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	app.MetricsHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, needle := range []string{
		"sample_api_http_requests_total",
		"sample_api_http_request_duration_seconds_bucket",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("metrics body does not contain %q", needle)
		}
	}
}

func TestRootResponseShape(t *testing.T) {
	t.Parallel()

	app := newTestServer()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	app.PublicHandler().ServeHTTP(recorder, request)

	var body response
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Service != "sample-api" || body.Status != "ok" || body.Pod != "test-pod" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func newTestServer() *Server {
	return New(Config{
		PodName:         "test-pod",
		CPUWorkDuration: time.Millisecond,
		SlowDelay:       time.Millisecond,
		Logger:          slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Registry:        prometheus.NewRegistry(),
	})
}
