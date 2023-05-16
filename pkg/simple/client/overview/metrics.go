package overview

import (
	"context"
	"fmt"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v12 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/apiserver/query"
	resourcev1beta1 "kubesphere.io/kubesphere/pkg/models/resources/v1beta1"
)

type Counter interface {
	RegisterResource(options ...RegisterOption)
	GetMetrics(metricNames []string, namespace, prefix string, queryParam *query.Query) (*MetricResults, error)
}

var _ Counter = &metricsCounter{}

type metricsCounter struct {
	registerTypeMap map[string][]reflect.Type
	resourceManager resourcev1beta1.ResourceManager
}

func New(resourceManager resourcev1beta1.ResourceManager) Counter {
	collector := &metricsCounter{
		registerTypeMap: make(map[string][]reflect.Type),
		resourceManager: resourceManager,
	}

	return collector
}

func (r *metricsCounter) RegisterResource(options ...RegisterOption) {
	for _, o := range options {
		r.register(o)
	}
}

func (r *metricsCounter) register(option RegisterOption) {
	refTypes := make([]reflect.Type, 0)
	for _, t := range option.Type {
		refTypes = append(refTypes, reflect.TypeOf(t))
	}

	r.registerTypeMap[option.MetricsName] = refTypes
}

func (r *metricsCounter) GetMetrics(metricNames []string, namespace, prefix string, queryParam *query.Query) (*MetricResults, error) {
	result := &MetricResults{Results: make([]Metric, 0)}
	for _, n := range metricNames {
		metric, err := r.collect(n, namespace, queryParam)
		if err != nil {
			return nil, err
		}

		if prefix != "" {
			metric.MetricName = fmt.Sprintf("%s_%s", prefix, metric.MetricName)
		}
		result.Results = append(result.Results, *metric)

	}

	return result, nil
}

type RegisterOption struct {
	MetricsName string
	Type        []client.ObjectList
}

func (r *metricsCounter) collect(metricName string, namespace string, queryParam *query.Query) (*Metric, error) {
	metric := &Metric{
		MetricName: metricName,
		Data: MetricData{
			ResultType: "vector",
			Result:     make([]MetricValue, 0),
		},
	}

	for _, t := range r.registerTypeMap[metricName] {
		objVal := reflect.New(t.Elem())
		objList := objVal.Interface().(client.ObjectList)
		err := r.resourceManager.List(context.Background(), namespace, queryParam, objList)
		if err != nil {
			return nil, err
		}

		value := MetricValue{
			Value: []interface{}{
				time.Now().Unix(),
				meta.LenList(objList),
			},
		}
		metric.Data.Result = append(metric.Data.Result, value)
	}

	return metric, nil
}

type MetricResults struct {
	Results []Metric `json:"results"`
}

type Metric struct {
	MetricName string     `json:"metric_name"`
	Data       MetricData `json:"data"`
}

type MetricData struct {
	ResultType string        `json:"resultType"`
	Result     []MetricValue `json:"result"`
}

type MetricValue struct {
	Value []interface{} `json:"value"`
}

func NewDefaultRegisterOptions() []RegisterOption {
	options := []RegisterOption{
		{
			MetricsName: NamespaceCount,
			Type:        []client.ObjectList{&v12.NamespaceList{}},
		},
		{
			MetricsName: PodCount,
			Type:        []client.ObjectList{&v12.PodList{}},
		},
		{
			MetricsName: DeploymentCount,
			Type:        []client.ObjectList{&appsv1.DeploymentList{}},
		},
		{
			MetricsName: StatefulSetCount,
			Type:        []client.ObjectList{&appsv1.StatefulSetList{}},
		},
		{
			MetricsName: DaemonSetCount,
			Type:        []client.ObjectList{&appsv1.DaemonSetList{}},
		},
		{
			MetricsName: JobCount,
			Type:        []client.ObjectList{&batchv1.JobList{}},
		},
		{
			MetricsName: CronJobCount,
			Type:        []client.ObjectList{&batchv1.CronJobList{}},
		},
		{
			MetricsName: ServiceCount,
			Type:        []client.ObjectList{&v12.ServiceList{}},
		},
		{
			MetricsName: IngressCount,
			Type:        []client.ObjectList{&v1.IngressList{}},
		},
		{
			MetricsName: PersistentVolumeCount,
			Type:        []client.ObjectList{&v12.PersistentVolumeList{}},
		},
		{
			MetricsName: GlobalRoleCount,
			Type:        []client.ObjectList{&iamv1beta1.GlobalRoleList{}},
		},
		{
			MetricsName: WorkspaceRoleCount,
			Type:        []client.ObjectList{&iamv1beta1.WorkspaceRoleList{}},
		},
		{
			MetricsName: RoleCount,
			Type:        []client.ObjectList{&iamv1beta1.RoleList{}},
		},
		{
			MetricsName: ClusterRoleCount,
			Type:        []client.ObjectList{&iamv1beta1.ClusterRoleList{}},
		},
		{
			MetricsName: UserCount,
			Type:        []client.ObjectList{&iamv1alpha2.UserList{}},
		},
		{
			MetricsName: GlobalRoleBindingCount,
			Type:        []client.ObjectList{&iamv1beta1.GlobalRoleBindingList{}},
		},
		{
			MetricsName: ClusterRoleBindingCount,
			Type:        []client.ObjectList{&iamv1beta1.ClusterRoleBindingList{}},
		},
		{
			MetricsName: RoleBindingCount,
			Type:        []client.ObjectList{&iamv1beta1.RoleBindingList{}},
		},
		{
			MetricsName: WorkspaceRoleBindingCount,
			Type:        []client.ObjectList{&iamv1beta1.WorkspaceRoleBindingList{}},
		},
	}
	return options
}
