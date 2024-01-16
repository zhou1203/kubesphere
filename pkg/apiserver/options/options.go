package options

import (
	"kubesphere.io/utils/s3"

	"kubesphere.io/kubesphere/pkg/apiserver/auditing"
	"kubesphere.io/kubesphere/pkg/apiserver/authentication"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization"
	"kubesphere.io/kubesphere/pkg/models/terminal"
	"kubesphere.io/kubesphere/pkg/multicluster"
	"kubesphere.io/kubesphere/pkg/simple/client/cache"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
)

type Options struct {
	KubernetesOptions     *k8s.Options
	CacheOptions          *cache.Options
	AuthenticationOptions *authentication.Options
	AuthorizationOptions  *authorization.Options
	MultiClusterOptions   *multicluster.Options
	AuditingOptions       *auditing.Options
	TerminalOptions       *terminal.Options
	S3Options             *s3.Options
}
