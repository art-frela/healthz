package healthz

import (
	"errors"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	reLabel       = regexp.MustCompile(`(?m)variableLabels: {scope,dest}`)
	errMissLabels = errors.New("unexpected labels, need scope,dest")
)

// validateMetricLabels checks that the metric has label "scope" and "dest".
func validateMetricLabels(metric *prometheus.GaugeVec) error {
	ch := make(chan *prometheus.Desc, 1)
	metric.Describe(ch)
	desc := <-ch

	if reLabel.MatchString(desc.String()) {
		return nil
	}

	return errMissLabels
}
