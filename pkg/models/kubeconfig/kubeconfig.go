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

package kubeconfig

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/klog/v2"
	iamv1beta1 "kubesphere.io/api/iam/v1beta1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/scheme"
	"kubesphere.io/kubesphere/pkg/utils/pkiutil"
)

const (
	inClusterCAFilePath  = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	configMapPrefix      = "kubeconfig-"
	kubeconfigNameFormat = configMapPrefix + "%s"
	defaultClusterName   = "local"
	defaultNamespace     = "default"
	kubeconfigFileName   = "config"
	configMapKind        = "ConfigMap"
	configMapAPIVersion  = "v1"
	privateKeyAnnotation = "kubesphere.io/private-key"
	residual             = 72 * time.Hour
)

type Interface interface {
	GetKubeConfig(username string) (string, error)
	CreateKubeConfig(user *iamv1beta1.User) error
	UpdateKubeConfig(username string, csr *certificatesv1.CertificateSigningRequest) error
}

type operator struct {
	reader    runtimeclient.Reader
	writer    runtimeclient.Writer
	config    *rest.Config
	masterURL string
}

func NewOperator(client runtimeclient.Client, config *rest.Config) Interface {
	return &operator{writer: client, reader: client, config: config}
}

func NewReadOnlyOperator(reader runtimeclient.Reader, masterURL string) Interface {
	return &operator{reader: reader, masterURL: masterURL}
}

// CreateKubeConfig Create kubeconfig configmap in KubeSphereControlNamespace for the specified user
func (o *operator) CreateKubeConfig(user *iamv1beta1.User) error {
	configName := fmt.Sprintf(kubeconfigNameFormat, user.Name)

	secret := &corev1.Secret{}
	err := o.reader.Get(context.Background(),
		types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: configName}, secret)

	// already exist and cert will not expire in 3 days
	if err == nil && !isExpired(secret, user.Name) {
		return nil
	}

	// internal error
	if err != nil && !errors.IsNotFound(err) {
		klog.Error(err)
		return err
	}

	// create a new CSR
	var ca []byte
	if len(o.config.CAData) > 0 {
		ca = o.config.CAData
	} else {
		ca, err = os.ReadFile(inClusterCAFilePath)
		if err != nil {
			klog.Errorln(err)
			return err
		}
	}

	if err = o.createCSR(user.Name); err != nil {
		klog.Errorln(err)
		return err
	}

	currentContext := fmt.Sprintf("%s@%s", user.Name, defaultClusterName)
	config := clientcmdapi.Config{
		Kind:        configMapKind,
		APIVersion:  configMapAPIVersion,
		Preferences: clientcmdapi.Preferences{},
		Clusters: map[string]*clientcmdapi.Cluster{defaultClusterName: {
			Server:                   o.config.Host,
			InsecureSkipTLSVerify:    false,
			CertificateAuthorityData: ca,
		}},
		Contexts: map[string]*clientcmdapi.Context{currentContext: {
			Cluster:   defaultClusterName,
			AuthInfo:  user.Name,
			Namespace: defaultNamespace,
		}},
		CurrentContext: currentContext,
	}

	kubeconfig, err := clientcmd.Write(config)
	if err != nil {
		klog.Error(err)
		return err
	}

	// update configmap if it already exists.
	if !secret.CreationTimestamp.IsZero() {
		secret.StringData = map[string]string{kubeconfigFileName: string(kubeconfig)}
		if err = o.writer.Update(context.Background(), secret); err != nil {
			klog.Errorln(err)
			return err
		}
		return nil
	}

	// create a new config
	secret = &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       configMapKind,
			APIVersion: configMapAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      configName,
			Namespace: constants.KubeSphereNamespace,
			Labels:    map[string]string{constants.UsernameLabelKey: user.Name},
		},
		StringData: map[string]string{kubeconfigFileName: string(kubeconfig)},
	}

	if err = controllerutil.SetControllerReference(user, secret, scheme.Scheme); err != nil {
		klog.Errorln(err)
		return err
	}

	if err = o.writer.Create(context.Background(), secret); err != nil {
		klog.Errorln(err)
		return err
	}

	return nil
}

// GetKubeConfig returns kubeconfig data for the specified user
func (o *operator) GetKubeConfig(username string) (string, error) {
	secretName := fmt.Sprintf(kubeconfigNameFormat, username)

	secret := &corev1.Secret{}
	if err := o.reader.Get(context.Background(),
		types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: secretName}, secret); err != nil {
		klog.Errorln(err)
		return "", err
	}

	data := []byte(secret.Data[kubeconfigFileName])
	kubeconfig, err := clientcmd.Load(data)
	if err != nil {
		klog.Errorln(err)
		return "", err
	}

	masterURL := o.masterURL
	// server host override
	if cluster := kubeconfig.Clusters[defaultClusterName]; cluster != nil && masterURL != "" {
		cluster.Server = masterURL
	}

	data, err = clientcmd.Write(*kubeconfig)
	if err != nil {
		klog.Errorln(err)
		return "", err
	}

	return string(data), nil
}

func (o *operator) createCSR(username string) error {
	csrConfig := &certutil.Config{
		CommonName:   username,
		Organization: nil,
		AltNames:     certutil.AltNames{},
		Usages:       []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	x509csr, x509key, err := pkiutil.NewCSRAndKey(csrConfig)
	if err != nil {
		klog.Errorln(err)
		return err
	}

	var csrBuffer, keyBuffer bytes.Buffer
	if err = pem.Encode(&keyBuffer, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(x509key)}); err != nil {
		klog.Errorln(err)
		return err
	}

	var csrBytes []byte
	if csrBytes, err = x509.CreateCertificateRequest(rand.Reader, x509csr, x509key); err != nil {
		klog.Errorln(err)
		return err
	}

	if err = pem.Encode(&csrBuffer, &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes}); err != nil {
		klog.Errorln(err)
		return err
	}

	csr := csrBuffer.Bytes()
	key := keyBuffer.Bytes()
	csrName := fmt.Sprintf("%s-csr-%d", username, time.Now().Unix())
	k8sCSR := &certificatesv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CertificateSigningRequest",
			APIVersion: "certificates.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        csrName,
			Labels:      map[string]string{constants.UsernameLabelKey: username},
			Annotations: map[string]string{privateKeyAnnotation: string(key)},
		},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:    csr,
			SignerName: certificatesv1.KubeAPIServerClientSignerName,
			Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageKeyEncipherment, certificatesv1.UsageClientAuth, certificatesv1.UsageDigitalSignature},
			Username:   username,
			Groups:     []string{user.AllAuthenticated},
		},
	}

	// create csr
	if err = o.writer.Create(context.Background(), k8sCSR); err != nil {
		klog.Errorln(err)
		return err
	}

	return nil
}

// UpdateKubeconfig Update writer key and writer certificate after CertificateSigningRequest has been approved
func (o *operator) UpdateKubeConfig(username string, csr *certificatesv1.CertificateSigningRequest) error {
	secretName := fmt.Sprintf(kubeconfigNameFormat, username)
	secret := &corev1.Secret{}
	if err := o.reader.Get(context.Background(),
		types.NamespacedName{Namespace: constants.KubeSphereNamespace, Name: secretName}, secret); err != nil {
		klog.Errorln(err)
		return err
	}
	secret = applyCert(secret, csr)
	if err := o.writer.Update(context.Background(), secret); err != nil {
		klog.Errorln(err)
		return err
	}
	return nil
}

func applyCert(secret *corev1.Secret, csr *certificatesv1.CertificateSigningRequest) *corev1.Secret {
	data := secret.Data[kubeconfigFileName]
	kubeconfig, err := clientcmd.Load(data)
	if err != nil {
		klog.Error(err)
		return secret
	}

	username := getControlledUsername(secret)
	privateKey := csr.Annotations[privateKeyAnnotation]
	clientCert := csr.Status.Certificate
	kubeconfig.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		username: {
			ClientKeyData:         []byte(privateKey),
			ClientCertificateData: clientCert,
		},
	}

	data, err = clientcmd.Write(*kubeconfig)
	if err != nil {
		klog.Error(err)
		return secret
	}

	secret.StringData = map[string]string{kubeconfigFileName: string(data)}
	return secret
}

func getControlledUsername(cm *corev1.Secret) string {
	for _, ownerReference := range cm.OwnerReferences {
		if ownerReference.Kind == iamv1beta1.ResourceKindUser {
			return ownerReference.Name
		}
	}
	return ""
}

// isExpired returns whether the writer certificate in kubeconfig is expired
func isExpired(cm *corev1.Secret, username string) bool {
	data := cm.Data[kubeconfigFileName]
	kubeconfig, err := clientcmd.Load(data)
	if err != nil {
		klog.Errorln(err)
		return true
	}
	authInfo, ok := kubeconfig.AuthInfos[username]
	if ok {
		clientCert, err := certutil.ParseCertsPEM(authInfo.ClientCertificateData)
		if err != nil {
			klog.Errorln(err)
			return true
		}
		for _, cert := range clientCert {
			if cert.NotAfter.Before(time.Now().Add(residual)) {
				return true
			}
		}
		return false
	}
	//ignore the kubeconfig, since it's not approved yet.
	return false
}
