package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	routeRoot    = "/"
	routeCPU     = "/cpu"
	routeError   = "/error"
	routeSlow    = "/slow"
	routeHealth  = "/healthz"
	routeReady   = "/readyz"
	routeUnknown = "unknown"
)

var requestDurationBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type Config struct {
	PodName         string
	CPUWorkDuration time.Duration
	SlowDelay       time.Duration
	DrainDelay      time.Duration
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
	Registry        *prometheus.Registry
}

type Server struct {
	podName         string
	cpuWorkDuration time.Duration
	slowDelay       time.Duration
	drainDelay      time.Duration
	shutdownTimeout time.Duration
	logger          *slog.Logger
	registry        *prometheus.Registry
	requests        *prometheus.CounterVec
	duration        *prometheus.HistogramVec
	ready           atomic.Bool
}

type response struct {
	Service  string `json:"service"`
	Status   string `json:"status"`
	Pod      string `json:"pod"`
	Delay    string `json:"delay,omitempty"`
	Work     string `json:"work,omitempty"`
	Checksum uint64 `json:"checksum,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

func New(config Config) *Server {
	if config.PodName == "" {
		config.PodName = "unknown"
	}
	if config.CPUWorkDuration <= 0 {
		config.CPUWorkDuration = 250 * time.Millisecond
	}
	if config.SlowDelay <= 0 {
		config.SlowDelay = 2 * time.Second
	}
	if config.DrainDelay < 0 {
		config.DrainDelay = 0
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if config.Registry == nil {
		config.Registry = prometheus.NewRegistry()
	}
	config.Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	requests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sample_api",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests handled by sample-api.",
		},
		[]string{"method", "route", "status"},
	)
	duration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "sample_api",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "Duration of HTTP requests handled by sample-api.",
			Buckets:   requestDurationBuckets,
		},
		[]string{"method", "route"},
	)
	config.Registry.MustRegister(requests, duration)

	server := &Server{
		podName:         config.PodName,
		cpuWorkDuration: config.CPUWorkDuration,
		slowDelay:       config.SlowDelay,
		drainDelay:      config.DrainDelay,
		shutdownTimeout: config.ShutdownTimeout,
		logger:          config.Logger,
		registry:        config.Registry,
		requests:        requests,
		duration:        duration,
	}
	server.ready.Store(true)
	return server
}

func (s *Server) PublicHandler() http.Handler {
	return http.HandlerFunc(s.handlePublic)
}

func (s *Server) MetricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))
	return mux
}

func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

func (s *Server) Run(ctx context.Context, publicAddress, metricsAddress string) error {
	publicServer := &http.Server{
		Addr:              publicAddress,
		Handler:           s.PublicHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	metricsServer := &http.Server{
		Addr:              metricsAddress,
		Handler:           s.MetricsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 2)
	start := func(httpServer *http.Server) {
		go func() {
			err := httpServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	s.ready.Store(true)
	start(publicServer)
	start(metricsServer)
	s.logger.Info("servers started", "public_address", publicAddress, "metrics_address", metricsAddress)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	s.ready.Store(false)
	if s.drainDelay > 0 {
		time.Sleep(s.drainDelay)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	return errors.Join(publicServer.Shutdown(shutdownCtx), metricsServer.Shutdown(shutdownCtx))
}

func (s *Server) handlePublic(writer http.ResponseWriter, request *http.Request) {
	requestID := requestID(request.Header.Get("X-Request-ID"))
	writer.Header().Set("X-Request-ID", requestID)

	route := classifyRoute(request.URL.Path)
	if route == routeHealth || route == routeReady {
		s.handleHealth(writer, request, route)
		return
	}

	started := time.Now()
	recorder := &statusRecorder{ResponseWriter: writer}
	s.handleApplication(recorder, request, route)
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}

	elapsed := time.Since(started)
	status := strconv.Itoa(recorder.status)
	s.requests.WithLabelValues(request.Method, route, status).Inc()
	s.duration.WithLabelValues(request.Method, route).Observe(elapsed.Seconds())
	s.logger.Info(
		"http request",
		"request_id", requestID,
		"method", request.Method,
		"route", route,
		"status", recorder.status,
		"duration_ms", elapsed.Milliseconds(),
		"pod", s.podName,
	)
}

func (s *Server) handleHealth(writer http.ResponseWriter, request *http.Request, route string) {
	if !methodAllowed(request.Method) {
		methodNotAllowed(writer)
		return
	}
	if route == routeReady && !s.ready.Load() {
		writeJSON(writer, http.StatusServiceUnavailable, errorResponse{Error: "not ready"})
		return
	}
	writeJSON(writer, http.StatusOK, response{
		Service: "sample-api",
		Status:  "ok",
		Pod:     s.podName,
	})
}

func (s *Server) handleApplication(writer http.ResponseWriter, request *http.Request, route string) {
	if !methodAllowed(request.Method) {
		methodNotAllowed(writer)
		return
	}

	switch route {
	case routeRoot:
		writeJSON(writer, http.StatusOK, response{
			Service: "sample-api",
			Status:  "ok",
			Pod:     s.podName,
		})
	case routeSlow:
		time.Sleep(s.slowDelay)
		writeJSON(writer, http.StatusOK, response{
			Service: "sample-api",
			Status:  "ok",
			Pod:     s.podName,
			Delay:   s.slowDelay.String(),
		})
	case routeCPU:
		checksum := burnCPU(s.cpuWorkDuration)
		writeJSON(writer, http.StatusOK, response{
			Service:  "sample-api",
			Status:   "ok",
			Pod:      s.podName,
			Work:     s.cpuWorkDuration.String(),
			Checksum: checksum,
		})
	case routeError:
		writeJSON(writer, http.StatusInternalServerError, errorResponse{Error: "intentional sample error"})
	default:
		http.NotFound(writer, request)
	}
}

func classifyRoute(path string) string {
	switch path {
	case routeRoot:
		return routeRoot
	case routeCPU:
		return routeCPU
	case routeError:
		return routeError
	case routeSlow:
		return routeSlow
	case routeHealth:
		return routeHealth
	case routeReady:
		return routeReady
	default:
		return routeUnknown
	}
}

func methodAllowed(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func methodNotAllowed(writer http.ResponseWriter) {
	writer.Header().Set("Allow", "GET, HEAD")
	writeJSON(writer, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(body); err != nil {
		http.Error(writer, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}

func requestID(value string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}

	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}

func burnCPU(duration time.Duration) uint64 {
	deadline := time.Now().Add(duration)
	var checksum uint64 = 1469598103934665603
	for time.Now().Before(deadline) {
		for i := uint64(0); i < 10000; i++ {
			checksum ^= i
			checksum *= 1099511628211
		}
	}
	return checksum
}
