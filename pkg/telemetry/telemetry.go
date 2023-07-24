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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"kubesphere.io/kubesphere/pkg/telemetry/collector"
	"kubesphere.io/kubesphere/pkg/telemetry/store"
)

type telemetry struct {
	client     runtimeclient.Client
	options    *Options
	store      store.Store
	collectors []collector.Collector
}

func NewTelemetry(opts ...Option) manager.Runnable {
	t := &telemetry{
		options:    NewTelemetryOptions(),
		collectors: collector.Registered,
	}
	for _, o := range opts {
		o(t)
	}
	// set default store
	if t.store == nil {
		t.store = store.NewMultiStore(
			store.NewKSCloudStore(store.KSCloudOptions{
				ServerURL: t.options.KSCloudURL,
				Client:    t.client,
			}),
			store.NewClusterInfoStore(store.ClusterInfoOptions{
				LiveTime: t.options.ClusterInfoLiveTime,
				Client:   t.client,
			}))
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

// WithStore set store to save data
func WithStore(s store.Store) Option {
	return func(t *telemetry) {
		t.store = s
	}
}

// WithOptions set config data for telemetry
func WithOptions(opt *Options) Option {
	return func(t *telemetry) {
		t.options = opt
	}
}

func (t *telemetry) Start(ctx context.Context) error {
	go wait.Until(func() {
		if *t.options.Enabled {
			if err := t.collect(ctx); err != nil {
				klog.Errorf("collect data error %v", err)
			}
		}
	}, *t.options.Period, ctx.Done())
	return nil
}

func (t *telemetry) NeedLeaderElection() bool {
	return true
}

func (t *telemetry) collect(ctx context.Context) error {
	var data = make(map[string]interface{})
	data["ts"] = time.Now().UTC().Format(time.RFC3339)
	var collectionOpt = &collector.CollectorOpts{Client: t.client, Ctx: ctx}
	wg := sync.WaitGroup{}
	for _, c := range t.collectors {
		wg.Add(1)
		go func(lc collector.Collector) {
			defer wg.Done()
			data[lc.RecordKey()] = lc.Collect(collectionOpt)
		}(c)
	}
	wg.Wait()
	if err := t.store.Save(ctx, data); err != nil {
		return err
	}
	return nil
}
