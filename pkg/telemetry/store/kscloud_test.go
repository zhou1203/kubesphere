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

package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// create a mock httpserver for qingcloud
func httptestServer() *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// validate method
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/apis/auth/v1/gettoken": // get token
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("{\"token_type\":\"Bearer\",\"expires_in\":21600,\"access_token\":\"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9\",\"refresh_token\":\"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9\",\"id\":\"1234567\",\"login_count\":17}")); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
		case "/apis/telemetry/v1/clusterinfos": // save data
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	})

	return httptest.NewServer(handler)
}

func TestHttpSave(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		endpoint string
		data     []byte
	}{
		{
			name: "http save success",
			url:  "https://clouddev.kubesphere.io",
			data: []byte("{\"clusters\":[{\"role\":\"host\",\"name\":\"host\",\"uid\":\"8c08b7a6-f057-48e4-90b9-cc72308e87bd\",\"nid\":\"2302d38a-c75b-46ee-b90a-a207f88de731\",\"ksVersion\":\"v4.0.0-alpha.1.146+7e2b7dcadd9f56\",\"clusterVersion\":\"v1.25.4\",\"namespace\":6,\"nodes\":[{\"uid\":\"ebda2b55-46b6-4fd3-9566-231196cfbbde\",\"name\":\"dev\",\"role\":[\"control-plane\",\"worker\"],\"arch\":\"amd64\",\"containerRuntime\":\"containerd://1.6.4\",\"kernel\":\"5.15.0-53-generic\",\"kubeProxy\":\"v1.25.4\",\"kubelet\":\"v1.25.4\",\"os\":\"linux\",\"osImage\":\"Ubuntu 22.04.1 LTS\"}]}],\"extension\":[],\"platform\":{\"workspace\":1,\"user\":1},\"ts\":\"2023-06-02T09:54:12Z\"}"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptestServer()
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			dm := make(map[string]interface{})
			if err := json.Unmarshal(tt.data, &dm); err != nil {
				t.Error(err)
				return
			}
			if err := NewKSCloudStore(KSCloudOptions{
				ServerURL: &server.URL,
				//ServerURL: &tt.url,
				Client: fake.NewClientBuilder().Build(),
			}).Save(ctx, dm); err != nil {
				t.Error(err)
			}
		})
	}
}
