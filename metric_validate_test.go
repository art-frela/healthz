package healthz

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/stretchr/testify/assert"
)

func Test_validateMetricLabels(t *testing.T) {
	m1 := promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "test_m1",
			Help: "Test metric",
		},
		[]string{"scope", "dest"},
	)
	m2 := promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "test_m2",
			Help: "Test metric",
		},
		[]string{"scope", "dest", "foo"},
	)

	tests := []struct {
		name    string
		metric  *prometheus.GaugeVec
		wantErr bool
	}{
		{
			name:    "test.1 ok valid",
			metric:  m1,
			wantErr: false,
		},
		{
			name:    "test.2 err more labels",
			metric:  m2,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMetricLabels(tt.metric)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
