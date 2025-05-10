package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/art-frela/healthz"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const stopTimeout = time.Second * 5

// metric for update
var serviceUp = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Name:        "service_up",
		Help:        "The example metric",
		ConstLabels: prometheus.Labels{"foo": "bar"},
	},
	[]string{"scope", "dest"},
)

// healthz.HealthCheckable simple implementation.
type SomeExtDependency struct {
	healthErr error
	scope     string
	dest      string
}

func (sed *SomeExtDependency) Health(ctx context.Context) error { return sed.healthErr }
func (sed *SomeExtDependency) Scope() string                    { return sed.scope }
func (sed *SomeExtDependency) Dest() string                     { return sed.dest }

// some http controller.
type Controller struct {
	//...
	hlz *healthz.Inspector
	srv *http.Server
}

func (c *Controller) start(ctx context.Context, hostPort string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz/startup", c.hlz.HealthHandler(healthz.GroupStartup, false, nil))
	mux.HandleFunc("/healthz/live", c.hlz.HealthHandler(healthz.GroupLive, false, nil))
	mux.HandleFunc("/healthz/ready", c.hlz.HealthHandler(healthz.GroupReady, true, nil))

	mux.HandleFunc("/metrics", promhttp.Handler().ServeHTTP)

	server := &http.Server{
		Addr:    hostPort,
		Handler: mux,
	}

	c.srv = server

	go func() {
		log.Printf("starting http server on %s", hostPort)

		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			log.Println("http server closed")
		} else if err != nil {
			log.Fatalf("http server closed error: %s", err)
		}
	}()

	c.hlz.Start(ctx)
}

func (c *Controller) stop() {
	if c.srv == nil {
		return
	}

	stopCTX, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()

	c.hlz.Stop(stopCTX)

	if err := c.srv.Shutdown(stopCTX); err != nil {
		log.Printf("http server shutdown error: %s", err)
	}
}

func main() {
	port := flag.String("p", ":6060", "host:port for http")
	flag.Parse()

	// bootstrap dependency
	someDepErr := &SomeExtDependency{
		healthErr: errors.New("some err"),
		scope:     "database",
		dest:      "host-1:5432/db_1",
	}

	someDepOK := &SomeExtDependency{
		scope: "kafka",
		dest:  "host-1:8431",
	}

	// init healthz.Inspector
	hlz := healthz.New(
		healthz.HealthCheckTarget{
			Service: someDepErr,
			Groups:  healthz.GroupReady,
		},
		healthz.HealthCheckTarget{
			Service: someDepOK,
			Groups:  healthz.AllGroups,
		},
	)

	if err := healthz.WithMetric(serviceUp)(hlz); err != nil {
		log.Fatalf("add metric to health inspector: %s", err)
	}

	// use healthz.Inspector in controller
	c := &Controller{
		hlz: hlz,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c.start(ctx, *port)

	<-ctx.Done()

	c.stop()
}

// GET /metrics
// # HELP service_up The example metric
// # TYPE service_up gauge
// service_up{dest="host-1:5432/db_1",foo="bar",scope="database"} 0
// service_up{dest="host-1:8431",foo="bar",scope="kafka"} 1

// GET /healthz/startup
// OK

// GET /healthz/live
// OK

// GET /healthz/ready
// Unhealthy
