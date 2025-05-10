package healthz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
)

const (
	defCheckPeriod  = time.Second * 15
	shutdownTimeout = time.Second * 15
)

var (
	errMissTargets      = errors.New("miss targets")
	errMissGroup        = errors.New("miss group, allow only combinations from 1, 2, 4")
	errEmptyGroup       = errors.New("empty probe group")
	errWrongCheckPeriod = errors.New("incorrect check period")
)

// ProbeGroup - Bit Mask Verification Groups.
type ProbeGroup uint8

func (pg ProbeGroup) validate() error {
	if pg == 0 {
		return errEmptyGroup
	}

	if (pg & AllGroups) != pg {
		return errMissGroup
	}

	return nil
}

const (
	GroupCommon  ProbeGroup = 1 << iota // 1
	GroupStartup                        // 2
	GroupLive                           // 4
	GroupReady                          // 8
	//
	AllGroups ProbeGroup = GroupCommon | GroupStartup | GroupLive | GroupReady // 15
)

// HealthCheckable - Interface of the verified service.
type HealthCheckable interface {
	Health(ctx context.Context) error // The main health test
	Scope() string                    // Group/Category (for example: "Database", "Cache", "Kafka")
	Dest() string                     // A specific resource or (for example: "Redis-Primary", "Postgres-12", "kafka-1.domain.local:8321")
}

// HealthCheckTarget - container for the service and its groups.
type HealthCheckTarget struct {
	Service HealthCheckable
	Groups  ProbeGroup // Bit mask of groups
}

type Option func(i *Inspector) error

// Inspector - the main control structure.
type Inspector struct {
	targets       []HealthCheckTarget
	stopCh        chan struct{}
	confirmStopCh chan struct{}
	metric        *prometheus.GaugeVec
	checkPeriod   time.Duration
	data          unsafe.Pointer
}

func New(targets ...HealthCheckTarget) *Inspector {
	return &Inspector{
		targets:     targets,
		checkPeriod: defCheckPeriod,
		data:        unsafe.Pointer(newHealthResult()),
	}
}

func WithTargets(targets ...HealthCheckTarget) Option {
	return func(i *Inspector) error {
		if len(targets) == 0 {
			return errMissTargets
		}

		for _, target := range targets {
			if err := target.Groups.validate(); err != nil {
				return err
			}
		}

		i.targets = targets

		return nil
	}
}

func WithMetric(metric *prometheus.GaugeVec) Option {
	return func(i *Inspector) error {
		if metric == nil {
			i.metric = nil

			return nil
		}

		if err := validateMetricLabels(metric); err != nil {
			return err
		}

		i.metric = metric

		return nil
	}
}

func WithCheckPeriod(p time.Duration) Option {
	return func(i *Inspector) error {
		if p <= 0 {
			return errWrongCheckPeriod
		}

		i.checkPeriod = p

		return nil
	}
}

func (i *Inspector) CheckGroup(group ProbeGroup, needAllHealthy bool) error {
	res := i.get()
	return res.health(group, needAllHealthy)
}

var DefResponseProcessor = func(err error) []byte {
	if err != nil {
		return []byte("Unhealthy")
	}

	return []byte("OK")
}

func (i *Inspector) HealthHandler(group ProbeGroup, needAllHealthy bool, toResponse func(error) []byte) http.HandlerFunc {
	if toResponse == nil {
		toResponse = DefResponseProcessor
	}

	return func(w http.ResponseWriter, _ *http.Request) {
		if err := i.CheckGroup(group, needAllHealthy); err != nil {
			w.WriteHeader(http.StatusOK)
			w.Write(toResponse(err))

			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(toResponse(nil))
	}
}

func (i *Inspector) Start(ctx context.Context) error {
	i.stopCh = make(chan struct{})
	i.confirmStopCh = make(chan struct{})

	go i.start(ctx)

	return nil
}

func (i *Inspector) Stop(ctx context.Context) error {
	if i.stopCh == nil {
		return nil
	}

	close(i.stopCh)
	i.stopCh = nil

	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	select {
	case <-i.confirmStopCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timeout: %w", ctx.Err())
	}
}

func (i *Inspector) start(ctx context.Context) {
	ticker := time.NewTicker(i.checkPeriod)
	defer ticker.Stop()
	defer close(i.confirmStopCh) // waiting all job to be done

	i.check(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-i.stopCh:
			return
		case <-ticker.C:
			i.check(ctx)
		}
	}
}

type serviceCheckResult struct {
	target HealthCheckTarget
	err    error
}

func (i *Inspector) check(ctx context.Context) {
	result := healthResult{}

	g, ctx := errgroup.WithContext(ctx)

	chResult := make(chan serviceCheckResult, 1)

	for _, target := range i.targets {
		g.Go(func() error {
			chResult <- serviceCheckResult{target: target, err: target.Service.Health(ctx)}

			return nil
		})
	}

	go func() {
		_ = g.Wait()

		close(chResult)
	}()

	for resTarget := range chResult {
		result.add(resTarget)
		i.updateMetric(resTarget.target.Service, resTarget.err)
	}

	pointer := unsafe.Pointer(&result)
	atomic.StorePointer(&i.data, pointer)
}

func (i *Inspector) get() *healthResult {
	pointer := atomic.LoadPointer(&i.data)
	data := (*healthResult)(pointer)

	return data
}

func (i *Inspector) updateMetric(svc HealthCheckable, err error) {
	if i.metric == nil {
		return
	}

	healthy := 0.0
	if err == nil {
		healthy = 1.0
	}

	i.metric.WithLabelValues(svc.Scope(), svc.Dest()).Set(healthy)
}
