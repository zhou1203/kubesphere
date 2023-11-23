/*
Copyright 2020 KubeSphere Authors

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

package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"k8s.io/klog/v2"

	"kubesphere.io/kubesphere/pkg/apiserver/auditing/internal"
)

const (
	GetSenderTimeout  = time.Second
	SendTimeout       = time.Second * 3
	DefaultSendersNum = 100

	WebhookURL = "https://kube-auditing-webhook-svc.kubesphere-logging-system.svc:6443/audit/webhook/event"
)

type backend struct {
	url              string
	senderCh         chan interface{}
	client           http.Client
	sendTimeout      time.Duration
	getSenderTimeout time.Duration
}

func NewBackend(url string, sendersNum int) internal.Backend {

	b := backend{
		url:              url,
		getSenderTimeout: GetSenderTimeout,
		sendTimeout:      SendTimeout,
	}

	if len(b.url) == 0 {
		b.url = WebhookURL
	}

	num := sendersNum
	if num == 0 {
		num = DefaultSendersNum
	}
	b.senderCh = make(chan interface{}, num)

	b.client = http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		Timeout: b.sendTimeout,
	}

	return &b
}

func (b *backend) ProcessEvents(events ...[]byte) {
	go b.sendEvents(events...)
}

func (b *backend) sendEvents(events ...[]byte) {
	ctx, cancel := context.WithTimeout(context.Background(), b.sendTimeout)
	defer cancel()

	stopCh := make(chan struct{})
	skipReturnSender := false

	send := func() {
		ctx, cancel := context.WithTimeout(context.Background(), b.getSenderTimeout)
		defer cancel()

		select {
		case <-ctx.Done():
			klog.Error("Get auditing event sender timeout")
			skipReturnSender = true
			return
		case b.senderCh <- struct{}{}:
		}

		start := time.Now()
		defer func() {
			stopCh <- struct{}{}
			klog.V(8).Infof("send %d auditing events used %d", len(events), time.Since(start).Milliseconds())
		}()

		var body bytes.Buffer
		for _, event := range events {
			if _, err := body.Write(event); err != nil {
				klog.Errorf("send auditing event error %s", err)
				return
			}
		}

		response, err := b.client.Post(b.url, "application/json", &body)
		if err != nil {
			klog.Errorf("send audit events error, %s", err)
			return
		}
		defer response.Body.Close()

		if response.StatusCode != http.StatusOK {
			klog.Errorf("send audit events error[%d]", response.StatusCode)
			return
		}
	}

	go send()

	defer func() {
		if !skipReturnSender {
			<-b.senderCh
		}
	}()

	select {
	case <-ctx.Done():
		klog.Error("send audit events timeout")
	case <-stopCh:
	}
}