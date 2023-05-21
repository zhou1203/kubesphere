//go:build exclude

// TODO refactor with  fake controller runtime client
/*
Copyright 2019 The KubeSphere Authors.

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

package secret

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"

	"kubesphere.io/kubesphere/pkg/apiserver/query"
	"kubesphere.io/kubesphere/pkg/models/resources/v1alpha3"
)

var testSecret = &v1.Secret{
	TypeMeta: metav1.TypeMeta{
		Kind:       "Secret",
		APIVersion: "v1",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name:            "prometheus-k8s",
		Namespace:       "kube-system",
		ResourceVersion: "1234567",
		Labels: map[string]string{
			"modifiedAt": "1670227209",
			"name":       "snapshot-controller",
			"owner":      "helm",
			"status":     "superseded",
			"version":    "2",
		},
	},
	Data: map[string][]byte{
		"testdata": []byte("thisisatestsecret"),
	},
	Type: "helm.sh/release.v1",
}

func BenchmarkContains(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if contains(testSecret, "metadata.labels.status!=superseded") {
			b.Error("test failed")
		}
	}

}

func BenchmarkDefaultListWith1000(b *testing.B) {
	s := &secretSearcher{}
	q := query.New()
	q.Filters[query.ParameterFieldSelector] = "metadata.resourceVersion=1234567"
	expectedListCount := rand.Intn(20)
	list := prepareList(testSecret, 1000, expectedListCount)

	for i := 0; i < b.N; i++ {
		list := v1alpha3.DefaultList(list, q, s.compare, s.filter)
		if list.TotalItems != expectedListCount {
			b.Error("test failed")
		}
	}
}

func BenchmarkDefaultListWith5000(b *testing.B) {
	s := &secretSearcher{}
	q := query.New()
	q.Filters[query.ParameterFieldSelector] = "metadata.resourceVersion=1234567"
	expectedListCount := rand.Intn(20)

	list := prepareList(testSecret, 5000, expectedListCount)
	for i := 0; i < b.N; i++ {
		list := v1alpha3.DefaultList(list, q, s.compare, s.filter)
		if list.TotalItems != expectedListCount {
			b.Error("test failed")
		}
	}
}

func BenchmarkDefaultListWith10000(b *testing.B) {
	s := &secretSearcher{}
	q := query.New()
	q.Filters[query.ParameterFieldSelector] = "metadata.resourceVersion=1234567"
	expectedListCount := rand.Intn(20)
	list := prepareList(testSecret, 100000, expectedListCount)
	for i := 0; i < b.N; i++ {
		list := v1alpha3.DefaultList(list, q, s.compare, s.filter)
		if list.TotalItems != expectedListCount {
			b.Error("test failed")
		}
	}
}

func BenchmarkDefaultListWith50000(b *testing.B) {
	s := &secretSearcher{}
	q := query.New()
	q.Filters[query.ParameterFieldSelector] = "metadata.resourceVersion=1234567"
	expectedListCount := rand.Intn(20)
	for i := 0; i < b.N; i++ {
		list := v1alpha3.DefaultList(prepareList(testSecret, 50000, expectedListCount), q, s.compare, s.filter)
		if list.TotalItems != expectedListCount {
			b.Error("test failed")
		}
	}
}

func prepareList(testSecret *v1.Secret, listLen, expected int) []runtime.Object {
	secretList := make([]runtime.Object, listLen)

	for i := 0; i < listLen; i++ {
		secret := testSecret.DeepCopy()
		secret.Name = rand.String(20)
		secret.ObjectMeta.ResourceVersion = rand.String(10)
		secretList[i] = secret
	}

	for i := 0; i < expected; i++ {
		secretList[rand.Intn(listLen-1)] = testSecret
	}

	return secretList
}
