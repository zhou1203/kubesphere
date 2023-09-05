package telemetry

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	telemetryv1alpha1 "kubesphere.io/api/telemetry/v1alpha1"

	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/telemetry/collector"
)

type testCollector struct {
	data     interface{}
	realTime int
	time     int
}

func (t testCollector) RecordKey() string {
	return "test"
}

func (t *testCollector) Collect(*collector.CollectorOpts) (interface{}, error) {
	t.time++
	if t.time == t.realTime {
		return t.data, nil
	}
	return t.data, fmt.Errorf("alway error")
}

func TestCollectRetry(t *testing.T) {
	testcases := []struct {
		name       string
		collectors []collector.Collector
		retryTime  int
	}{
		{
			name: "collector succeed",
			collectors: []collector.Collector{&testCollector{
				data:     "",
				realTime: 1,
			}},
			retryTime: 0,
		},
		{
			name: "collector retry",
			collectors: []collector.Collector{&testCollector{
				data:     "",
				realTime: 4,
			}},
			retryTime: 3,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tt := &telemetry{
				client:             fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(&telemetryv1alpha1.ClusterInfo{}).Build(),
				collectors:         tc.collectors,
				collectRetryPeriod: time.Second,
				Once:               sync.Once{},
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*50)
			defer cancel()
			err := tt.collect(ctx)
			if err != nil {
				t.Error(err)
			}
		})
	}
}
