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
	"time"

	"k8s.io/utils/pointer"
)

const (
	// defaultKSCloudURL for ksCloud.
	// test environment is https://clouddev.kubesphere.io. product environment is https://kubesphere.cloud
	defaultKSCloudURL = "https://kubesphere.cloud"

	// defaultPeriod is when to telemetry
	defaultPeriod = time.Hour * 24

	// defaultCusterInfoLiveTime for cluster info
	defaultCusterInfoLiveTime = time.Hour * 24 * 365
)

// Options is the config data for telemetry.
type Options struct {
	// KSCloudURL for kubesphere cloud
	KSCloudURL *string `json:"ksCloudURL,omitempty" yaml:"ksCloudURL,omitempty" mapstructure:"ksCloudURL"`

	// collect period
	Period *time.Duration `json:"period,omitempty" yaml:"period,omitempty" mapstructure:"period"`

	// history data live time
	ClusterInfoLiveTime *time.Duration `json:"clusterInfoLiveTime,omitempty" yaml:"clusterInfoLiveTime,omitempty" mapstructure:"clusterInfoLiveTime"`
}

func NewTelemetryOptions() *Options {
	return &Options{
		KSCloudURL:          pointer.String(defaultKSCloudURL),
		Period:              pointer.Duration(defaultPeriod),
		ClusterInfoLiveTime: pointer.Duration(defaultCusterInfoLiveTime),
	}
}
