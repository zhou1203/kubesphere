/*
Copyright 2023 The KubeSphere Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package telemetry

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"kubesphere.io/kubesphere/pkg/models/telemetry/collector"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	telemetryv1alpha1 "kubesphere.io/api/telemetry/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"kubesphere.io/kubesphere/pkg/models/marketplace"
)

const (
	cloudID            = "cloudId"
	defaultNameFormat  = "20060102150405"
	collectRetryPeriod = time.Minute * 10
)

type telemetry struct {
	client             runtimeclient.Client
	options            *Options
	collectors         []collector.Collector
	collectRetryPeriod time.Duration
	sync.Once
}

func NewTelemetry(opts ...Option) manager.Runnable {
	t := &telemetry{
		options:            NewTelemetryOptions(),
		collectors:         collector.Registered,
		collectRetryPeriod: collectRetryPeriod,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *telemetry) RegisterCollector(cs ...collector.Collector) {
	t.collectors = append(t.collectors, cs...)
}

// Option is a configuration option supplied to NewTelemetry.
type Option func(*telemetry)

// WithClient set kubernetes client to collector data.
func WithClient(cli runtimeclient.Client) Option {
	return func(t *telemetry) {
		t.client = cli
	}
}

// WithOptions set config data for telemetry
func WithOptions(opt *Options) Option {
	return func(t *telemetry) {
		t.options = opt
	}
}

func (t *telemetry) Start(ctx context.Context) error {
	t.Once.Do(func() {
		go wait.Until(func() {
			if err := t.collect(ctx); err != nil {
				klog.Errorf("collect data error %v", err)
			}
		}, *t.options.Period, ctx.Done())
	})
	return nil
}

func (t *telemetry) NeedLeaderElection() bool {
	return true
}

func (t *telemetry) collect(ctx context.Context) error {
	// Skip this collection When the interval since the last collection has not reached the options.Period
	if t.skip(ctx) {
		return nil
	}

	var data = make(map[string]interface{})
	data["ts"] = time.Now().UTC().Format(time.RFC3339)
	var collectionOpt = &collector.CollectorOpts{Client: t.client, Ctx: ctx}
	wg := sync.WaitGroup{}
	for _, c := range t.collectors {
		wg.Add(1)
		go func(lc collector.Collector) {
			defer wg.Done()
			collect, cancel := context.WithCancel(ctx)
			wait.Until(func() {
				value, err := lc.Collect(collectionOpt)
				if err != nil {
					// retry
					klog.Errorf("collector %s collect data error %v", lc.RecordKey(), err)
					return
				}
				data[lc.RecordKey()] = value
				cancel()
			}, t.collectRetryPeriod, collect.Done())
		}(c)
	}
	wg.Wait()

	// get cloudID
	if options, err := marketplace.LoadOptions(ctx, t.client); err != nil {
		klog.Warningf("cannot get cloudID %s", err)
		data[cloudID] = ""
	} else if options.Account != nil {
		data[cloudID] = options.Account.UserID
	} else {
		data[cloudID] = ""
	}
	if err := t.save(ctx, data); err != nil {
		return err
	}
	return nil
}

func (t *telemetry) skip(ctx context.Context) bool {
	clusterInfos := &telemetryv1alpha1.ClusterInfoList{}
	if err := t.client.List(ctx, clusterInfos); err != nil {
		return false
	}
	for _, clusterInfo := range clusterInfos.Items {
		if clusterInfo.CreationTimestamp.Add(*t.options.Period).After(time.Now()) {
			return true
		}
	}
	return false
}

func (t *telemetry) save(ctx context.Context, data map[string]interface{}) error {
	status := &telemetryv1alpha1.ClusterInfoStatus{}
	// convert data to status
	// data contains structured data in the form of a struct type. It is not registered in the runtime. It is preferable to use JSON conversion.
	if jsonBytes, err := json.Marshal(data); err != nil {
		return err
	} else {
		if err := json.Unmarshal(jsonBytes, status); err != nil {
			return err
		}
	}

	clusterInfo := &telemetryv1alpha1.ClusterInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name: status.TotalTime.UTC().Format(defaultNameFormat),
		},
	}

	// create clusterInfo
	if err := t.client.Create(ctx, clusterInfo.DeepCopy()); err != nil {
		return err
	}

	// update clusterInfo status
	newClusterInfo := clusterInfo.DeepCopy()
	newClusterInfo.Status = status
	if err := t.client.Status().Patch(ctx, newClusterInfo, runtimeclient.MergeFrom(clusterInfo.DeepCopy())); err != nil {
		return err
	}
	return nil
}
