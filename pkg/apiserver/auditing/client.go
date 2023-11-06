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

package auditing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	clusterv1alpha1 "kubesphere.io/api/cluster/v1alpha1"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"

	"github.com/google/uuid"
	v1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/apis/audit"
	"k8s.io/klog/v2"
	"kubesphere.io/api/iam/v1beta1"

	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"kubesphere.io/kubesphere/pkg/utils/iputil"
)

const (
	DefaultCacheCapacity = 10000
	CacheTimeout         = time.Second
)

type Auditing interface {
	Enabled() bool
	LogRequestObject(req *http.Request, info *request.RequestInfo) *Event
	LogResponseObject(e *Event, resp *ResponseCapture)
}

type auditing struct {
	kubernetesClient k8s.Client
	opts             *Options
	events           chan *Event
	backend          *Backend

	host    *Instance
	cluster string
}

func NewAuditing(kubernetesClient k8s.Client, opts *Options, stopCh <-chan struct{}) Auditing {

	a := &auditing{
		kubernetesClient: kubernetesClient,
		opts:             opts,
		events:           make(chan *Event, DefaultCacheCapacity),
		host:             getHostInstance(),
	}

	a.cluster = a.getClusterName()
	a.backend = NewBackend(opts, a.events, stopCh)
	return a
}

func getHostInstance() *Instance {
	addrs, err := net.InterfaceAddrs()
	hostip := ""
	if err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					hostip = ipnet.IP.String()
					break
				}
			}
		}
	}

	return &Instance{
		Name: os.Getenv("HOSTNAME"),
		IP:   hostip,
	}
}

func (a *auditing) getClusterName() string {
	ns, err := a.kubernetesClient.Kubernetes().CoreV1().Namespaces().Get(context.Background(), constants.KubeSphereNamespace, metav1.GetOptions{})
	if err != nil {
		klog.Error("get %s error: %s", constants.KubeSphereNamespace, err)
		return ""
	}

	if ns.Annotations != nil {
		return ns.Annotations[clusterv1alpha1.AnnotationClusterName]
	}

	return ""
}

func (a *auditing) getAuditLevel() audit.Level {
	if a.opts != nil &&
		a.opts.AuditLevel != "" {
		return a.opts.AuditLevel
	}

	return audit.LevelMetadata
}

func (a *auditing) Enabled() bool {

	level := a.getAuditLevel()
	return !level.Less(audit.LevelMetadata)
}

// If the request is not a standard request, or a resource request,
// or part of the audit information cannot be obtained through url,
// the function that handles the request can obtain Event from
// the context of the request, assign value to audit information,
// including name, verb, resource, subresource, message etc like this.
//
//	info, ok := request.AuditEventFrom(request.Request.Context())
//	if ok {
//		info.Verb = "post"
//		info.Name = created.Name
//	}

func (a *auditing) LogRequestObject(req *http.Request, info *request.RequestInfo) *Event {

	// Ignore the dryRun k8s request.
	if info.IsKubernetesRequest {
		if len(req.URL.Query()["dryRun"]) != 0 {
			klog.V(6).Infof("ignore dryRun request %s", req.URL.Path)
			return nil
		}
	}

	e := &Event{
		Instance:  a.host,
		Workspace: info.Workspace,
		Cluster:   a.cluster,
		Event: audit.Event{
			RequestURI:               info.Path,
			Verb:                     info.Verb,
			Level:                    a.getAuditLevel(),
			AuditID:                  types.UID(uuid.New().String()),
			Stage:                    audit.StageResponseComplete,
			ImpersonatedUser:         nil,
			UserAgent:                req.UserAgent(),
			RequestReceivedTimestamp: metav1.NowMicro(),
			Annotations:              nil,
			ObjectRef: &audit.ObjectReference{
				Resource:        info.Resource,
				Namespace:       info.Namespace,
				Name:            info.Name,
				UID:             "",
				APIGroup:        info.APIGroup,
				APIVersion:      info.APIVersion,
				ResourceVersion: info.ResourceScope,
				Subresource:     info.Subresource,
			},
		},
	}

	ips := make([]string, 1)
	ips[0] = iputil.RemoteIp(req)
	e.SourceIPs = ips

	user, ok := request.UserFrom(req.Context())
	if ok {
		e.User.Username = user.GetName()
		e.User.UID = user.GetUID()
		e.User.Groups = user.GetGroups()

		e.User.Extra = make(map[string]v1.ExtraValue)
		for k, v := range user.GetExtra() {
			e.User.Extra[k] = v
		}
	}

	if a.needAnalyzeRequestBody(e, req) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			klog.Error(err)
			return e
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewBuffer(body))

		if e.Level.GreaterOrEqual(audit.LevelRequest) {
			e.RequestObject = &runtime.Unknown{Raw: body}
		}

		// For resource creating request, get resource name from the request body.
		if info.Verb == "create" {
			obj := &Object{}
			if err := json.Unmarshal(body, obj); err == nil {
				e.ObjectRef.Name = obj.Name
			}
		}

		// for recording disable and enable user
		if e.ObjectRef.Resource == "users" && e.Verb == "update" {
			u := &v1beta1.User{}
			if err := json.Unmarshal(body, u); err == nil {
				if u.Status.State == v1beta1.UserActive {
					e.Verb = "enable"
				} else if u.Status.State == v1beta1.UserDisabled {
					e.Verb = "disable"
				}
			}
		}
	}

	return e
}

func (a *auditing) needAnalyzeRequestBody(e *Event, req *http.Request) bool {

	if req.ContentLength <= 0 {
		return false
	}

	if e.Level.GreaterOrEqual(audit.LevelRequest) {
		return true
	}

	if e.Verb == "create" {
		return true
	}

	// for recording disable and enable user
	if e.ObjectRef.Resource == "users" && e.Verb == "update" {
		return true
	}

	return false
}

func (a *auditing) LogResponseObject(e *Event, resp *ResponseCapture) {

	e.StageTimestamp = metav1.NowMicro()
	e.ResponseStatus = &metav1.Status{Code: int32(resp.StatusCode())}
	if e.Level.GreaterOrEqual(audit.LevelRequestResponse) {
		e.ResponseObject = &runtime.Unknown{Raw: resp.Bytes()}
	}

	a.cacheEvent(*e)
}

func (a *auditing) cacheEvent(e Event) {
	select {
	case a.events <- &e:
		return
	case <-time.After(CacheTimeout):
		klog.V(8).Infof("cache audit event %s timeout", e.AuditID)
		break
	}
}

type ResponseCapture struct {
	http.ResponseWriter
	wroteHeader bool
	status      int
	body        *bytes.Buffer
}

func NewResponseCapture(w http.ResponseWriter) *ResponseCapture {
	return &ResponseCapture{
		ResponseWriter: w,
		wroteHeader:    false,
		body:           new(bytes.Buffer),
	}
}

func (c *ResponseCapture) Header() http.Header {
	return c.ResponseWriter.Header()
}

func (c *ResponseCapture) Write(data []byte) (int, error) {
	c.WriteHeader(http.StatusOK)
	c.body.Write(data)
	return c.ResponseWriter.Write(data)
}

func (c *ResponseCapture) WriteHeader(statusCode int) {
	if !c.wroteHeader {
		c.status = statusCode
		c.ResponseWriter.WriteHeader(statusCode)
		c.wroteHeader = true
	}
}

func (c *ResponseCapture) Bytes() []byte {
	return c.body.Bytes()
}

func (c *ResponseCapture) StatusCode() int {
	return c.status
}

// Hijack implements the http.Hijacker interface.  This expands
// the Response to fulfill http.Hijacker if the underlying
// http.ResponseWriter supports it.
func (c *ResponseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := c.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("ResponseWriter doesn't support Hijacker interface")
	}
	return hijacker.Hijack()
}

// CloseNotify is part of http.CloseNotifier interface
func (c *ResponseCapture) CloseNotify() <-chan bool {
	//nolint:staticcheck
	return c.ResponseWriter.(http.CloseNotifier).CloseNotify()
}
