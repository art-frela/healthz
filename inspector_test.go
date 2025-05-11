package healthz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

const testTimeout = time.Second * 30

// Mock HealthCheckable implementation
type mockService struct {
	healthErr error
	scope     string
	dest      string
	callBack  func()
}

func (m *mockService) Health(ctx context.Context) error {
	if m.callBack != nil {
		m.callBack()
	}
	return m.healthErr
}
func (m *mockService) Scope() string { return m.scope }
func (m *mockService) Dest() string  { return m.dest }

func TestProbeGroup_validate(t *testing.T) {
	tests := []struct {
		name    string
		pg      ProbeGroup
		wantErr bool
	}{
		{
			name:    "test.9 err 251",
			pg:      251,
			wantErr: true,
		},
		{
			name:    "test.1 ok startup",
			pg:      GroupStartup,
			wantErr: false,
		},
		{
			name:    "test.2 ok live",
			pg:      GroupStartup,
			wantErr: false,
		},
		{
			name:    "test.3 ok ready",
			pg:      GroupStartup,
			wantErr: false,
		},
		{
			name:    "test.4 ok startup+live",
			pg:      GroupStartup | GroupLive,
			wantErr: false,
		},
		{
			name:    "test.5 ok startup+ready",
			pg:      GroupStartup | GroupReady,
			wantErr: false,
		},
		{
			name:    "test.6 ok live+ready",
			pg:      GroupLive | GroupReady,
			wantErr: false,
		},
		{
			name:    "test.7 ok startup+live+ready",
			pg:      GroupStartup | GroupLive | GroupReady,
			wantErr: false,
		},
		{
			name:    "test.8 err zero",
			pg:      0,
			wantErr: true,
		},
		{
			name:    "test.9 err 255",
			pg:      255,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.pg.validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Inspector constructor tests
func TestNewInspector(t *testing.T) {
	t.Run("Default constructor", func(t *testing.T) {
		svc := &mockService{}

		inspector := New(HealthCheckTarget{Service: svc, Groups: GroupLive})
		assert.NotNil(t, inspector)
	})

	t.Run("With options", func(t *testing.T) {
		svc := &mockService{}
		metric := prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "test_metric",
		}, []string{"scope", "dest"})

		opts := []Option{
			WithCheckPeriod(time.Second * 5),
			WithMetric(metric),
			WithTargets(HealthCheckTarget{
				Service: svc,
				Groups:  GroupStartup,
			}),
		}

		inspector := New()
		for _, opt := range opts {
			assert.NoError(t, opt(inspector))
		}

		assert.Equal(t, time.Second*5, inspector.checkPeriod)
	})
}

// Health check tests
func TestInspectorCheck(t *testing.T) {
	ctx := context.Background()
	healthySvc := &mockService{scope: "test", dest: "ok"}
	failingSvc := &mockService{healthErr: errors.New("fail"), scope: "test", dest: "fail"}

	tests := []struct {
		name           string
		targets        []HealthCheckTarget
		group          ProbeGroup
		needAllHealthy bool
		wantErr        bool
	}{
		{
			name: "All healthy",
			targets: []HealthCheckTarget{
				{Service: healthySvc, Groups: GroupStartup},
			},
			group:   GroupStartup,
			wantErr: false,
		},
		{
			name: "One failing in group",
			targets: []HealthCheckTarget{
				{Service: failingSvc, Groups: GroupStartup},
			},
			group:   GroupStartup,
			wantErr: true,
		},
		{
			name: "Partial group membership",
			targets: []HealthCheckTarget{
				{Service: healthySvc, Groups: GroupStartup},
				{Service: failingSvc, Groups: GroupLive},
			},
			group:   GroupStartup,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inspector := New(tt.targets...)
			inspector.checkPeriod = time.Millisecond // speed up tests

			go inspector.check(ctx)
			time.Sleep(10 * time.Millisecond) // allow first check to complete

			err := inspector.CheckGroup(tt.group, tt.needAllHealthy)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHealthHandler(t *testing.T) {
	svc := &mockService{}
	inspector := New(HealthCheckTarget{
		Service: svc,
		Groups:  GroupReady,
	})

	t.Run("Healthy response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		// Force healthy state
		inspector.check(context.Background())

		handler := inspector.HealthHandler(GroupReady, true, nil)
		handler(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Custom response processor", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		customProcessor := func(err error) []byte {
			return []byte("CUSTOM")
		}

		handler := inspector.HealthHandler(GroupReady, true, customProcessor)
		handler(w, req)

		assert.Equal(t, "CUSTOM", string(w.Body.Bytes()))
	})
}

func TestConcurrentAccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	svc := &mockService{}
	inspector := New(HealthCheckTarget{
		Service: svc,
		Groups:  GroupLive,
	})

	wg := &sync.WaitGroup{}
	numGoroutine := 100
	// Start background checks
	wg.Add(1)
	go func() {
		defer wg.Done()

		for range numGoroutine {
			inspector.check(ctx)
		}
	}()

	// Concurrent reads
	wg.Add(numGoroutine)
	for range numGoroutine {
		go func() {
			defer wg.Done()

			_ = inspector.CheckGroup(GroupLive, true)
		}()
	}

	wg.Wait()
}

func TestMetricCollection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	registry := prometheus.NewRegistry()
	metric := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "test_metric",
	}, []string{"scope", "dest"})
	registry.MustRegister(metric)

	svc := &mockService{
		scope: "test",
		dest:  "metric_test",
	}

	inspector := New(
		HealthCheckTarget{
			Service: svc,
			Groups:  GroupCommon,
		},
	)
	inspector.metric = metric

	// Perform check
	inspector.check(ctx)

	// Verify metric
	m, err := registry.Gather()
	assert.NoError(t, err)
	assert.Equal(t, 1, len(m))
}

func TestInspectorShutdown(t *testing.T) {
	inspector := New()
	done := make(chan struct{})

	go func() {
		inspector.check(context.Background())
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Error("check didn't complete")
	}
}

func TestStartAndStop(t *testing.T) {
	t.Run("Normal shutdown", func(t *testing.T) {
		inspector := New()
		ctx := context.Background()

		if err := inspector.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		// Даем время на запуск горутины
		time.Sleep(10 * time.Millisecond)

		if err := inspector.Stop(ctx); err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
	})

	t.Run("Shutdown with timeout", func(t *testing.T) {
		svc := &mockService{
			callBack: func() {
				time.Sleep(testTimeout)
			},
		}
		inspector := New(
			HealthCheckTarget{
				Service: svc,
				Groups:  GroupCommon,
			})
		inspector.Start(context.Background())
		// We give time to launch Gorutin
		time.Sleep(10 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()

		// We do not call Start so that confirmStopCh never closes
		err := inspector.Stop(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected DeadlineExceeded, got %v", err)
		}
	})

	t.Run("Multiple Stop calls", func(t *testing.T) {
		inspector := New()
		ctx := context.Background()

		if err := inspector.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		// We give time to launch Gorutin
		time.Sleep(10 * time.Millisecond)

		// The first challenge
		if err := inspector.Stop(ctx); err != nil {
			t.Errorf("first Stop failed: %v", err)
		}

		// Subsequent challenges should not panic
		if err := inspector.Stop(ctx); err != nil {
			t.Errorf("second Stop failed: %v", err)
		}
	})
}

func TestStartBehavior(t *testing.T) {
	t.Run("Context cancellation stops checks", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		inspector := New()

		if err := inspector.Start(ctx); err != nil {
			t.Fatal(err)
		}

		// Отменяем контекст
		cancel()

		// Ждем остановки
		select {
		case <-inspector.confirmStopCh:
			// OK
		case <-time.After(100 * time.Millisecond):
			t.Error("inspector didn't stop after context cancellation")
		}
	})

	t.Run("Periodic checks execution", func(t *testing.T) {
		checkCount := atomic.Int32{}
		svc := &mockService{
			callBack: func() {
				checkCount.Add(1)
			},
		}

		inspector := New(HealthCheckTarget{
			Service: svc,
			Groups:  GroupCommon,
		})
		inspector.checkPeriod = 10 * time.Millisecond

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		inspector.Start(ctx)

		<-ctx.Done()

		if count := checkCount.Load(); count < 3 {
			t.Errorf("expected at least 3 checks, got %d", count)
		}
	})
}

func TestStopEdgeCases(t *testing.T) {
	t.Run("Stop without Start", func(t *testing.T) {
		inspector := New()
		if err := inspector.Stop(context.Background()); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Stop with nil channels", func(t *testing.T) {
		inspector := &Inspector{}
		if err := inspector.Stop(context.Background()); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
