/*
Copyright 2020 The KubeSphere Authors.

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

package auditing

import (
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apiserver/pkg/apis/audit"

	"kubesphere.io/kubesphere/pkg/utils/reflectutils"
)

type Options struct {
	Enable     bool   `json:"enable" yaml:"enable"`
	WebhookUrl string `json:"webhookUrl" yaml:"webhookUrl"`
	// The maximum concurrent senders which send auditing events to the auditing webhook.
	EventSendersNum int `json:"eventSendersNum" yaml:"eventSendersNum"`
	// The batch size of auditing events.
	EventBatchSize int `json:"eventBatchSize" yaml:"eventBatchSize"`
	// The batch interval of auditing events.
	EventBatchInterval time.Duration `json:"eventBatchInterval" yaml:"eventBatchInterval"`

	AuditLevel audit.Level `json:"auditLevel" yaml:"auditLevel"`
}

func NewAuditingOptions() *Options {
	return &Options{}
}

func (s *Options) ApplyTo(options *Options) {
	if s.WebhookUrl != "" {
		reflectutils.Override(options, s)
	}
}

func (s *Options) Validate() []error {
	errs := make([]error, 0)
	return errs
}

func (s *Options) AddFlags(fs *pflag.FlagSet, c *Options) {
	fs.BoolVar(&s.Enable, "auditing-enabled", c.Enable, "Enable auditing component or not. ")
	fs.StringVar(&s.WebhookUrl, "auditing-webhook-url", c.WebhookUrl, "Auditing wehook url")
	fs.IntVar(&s.EventSendersNum, "auditing-event-senders-num", c.EventSendersNum,
		"The maximum concurrent senders which send auditing events to the auditing webhook.")
	fs.IntVar(&s.EventBatchSize, "auditing-event-batch-size", c.EventBatchSize,
		"The batch size of auditing events.")
	fs.DurationVar(&s.EventBatchInterval, "auditing-event-batch-interval", c.EventBatchInterval,
		"The batch interval of auditing events.")
}
