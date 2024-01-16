package v2

import (
	"bytes"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	restful "github.com/emicklei/go-restful/v3"
	"github.com/go-openapi/strfmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"kubesphere.io/kubesphere/pkg/api"
	"kubesphere.io/kubesphere/pkg/constants"
	"kubesphere.io/kubesphere/pkg/utils/idutils"
)

type Attachment struct {
	AttachmentContent map[string]strfmt.Base64 `json:"attachment_content,omitempty"`
	AttachmentID      string                   `json:"attachment_id,omitempty"`
}

func (h *appHandler) DescribeAttachmentFromCM(req *restful.Request, resp *restful.Response, attachmentId string) {
	obj := corev1.ConfigMap{}
	err := h.client.Get(req.Request.Context(), runtimeclient.ObjectKey{
		Name:      attachmentId,
		Namespace: constants.KubeSphereNamespace,
	}, &obj)
	if requestDone(err, resp) {
		return
	}
	result := &Attachment{AttachmentID: attachmentId,
		AttachmentContent: map[string]strfmt.Base64{
			"raw": strfmt.Base64(obj.Data["raw"]),
		},
	}
	resp.WriteEntity(result)
}
func (h *appHandler) DescribeAttachment(req *restful.Request, resp *restful.Response) {
	attachmentId := req.PathParameter("attachment")
	if h.backingStoreClient == nil {
		h.DescribeAttachmentFromCM(req, resp, attachmentId)
		return
	}
	data, err := h.backingStoreClient.Read(attachmentId)
	if requestDone(err, resp) {
		return
	}
	result := &Attachment{AttachmentID: attachmentId,
		AttachmentContent: map[string]strfmt.Base64{
			"raw": data,
		},
	}
	resp.WriteEntity(result)
}
func (h *appHandler) CreateAttachmentToCM(req *restful.Request, resp *restful.Response, id string, data []byte) {
	obj := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id,
			Namespace: constants.KubeSphereNamespace,
		},
		Data: map[string]string{
			"raw": string(data),
		},
	}
	err := h.client.Create(req.Request.Context(), &obj)
	if err != nil {
		api.HandleBadRequest(resp, nil, err)
		return
	}
	klog.V(4).Infof("upload attachment success")
	att := &Attachment{AttachmentID: id}
	resp.WriteEntity(att)
}

func (h *appHandler) CreateAttachment(req *restful.Request, resp *restful.Response) {
	err := req.Request.ParseMultipartForm(10 << 20)
	if err != nil {
		api.HandleBadRequest(resp, nil, err)
		return
	}

	var att *Attachment
	// just save one attachment
	for fName := range req.Request.MultipartForm.File {
		f, _, err := req.Request.FormFile(fName)
		if err != nil {
			api.HandleBadRequest(resp, nil, err)
			return
		}
		data, _ := io.ReadAll(f)
		f.Close()
		id := idutils.GetUuid36("att")
		if h.backingStoreClient == nil {
			h.CreateAttachmentToCM(req, resp, id, data)
			return
		}

		err = h.backingStoreClient.Upload(id, id, bytes.NewBuffer(data), len(data))
		if err != nil {
			klog.Errorf("upload attachment failed, err: %s", err)
			api.HandleBadRequest(resp, nil, err)
			return
		}
		klog.V(4).Infof("upload attachment success")
		att = &Attachment{AttachmentID: id}
		break
	}

	resp.WriteEntity(att)
}
func (h *appHandler) DeleteAttachmentsFromCM(req *restful.Request, resp *restful.Response, ids []string) {
	for _, id := range ids {
		obj := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      id,
				Namespace: constants.KubeSphereNamespace,
			},
		}
		err := h.client.Delete(req.Request.Context(), &obj)
		if err != nil && apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			api.HandleInternalError(resp, nil, err)
			return
		}
	}
}
func (h *appHandler) DeleteAttachments(req *restful.Request, resp *restful.Response) {
	attachmentId := req.PathParameter("attachment")
	ids := strings.Split(attachmentId, ",")
	if h.backingStoreClient == nil {
		h.DeleteAttachmentsFromCM(req, resp, ids)
		return
	}
	for _, id := range ids {
		err := h.backingStoreClient.Delete(id)
		if err != nil {
			api.HandleInternalError(resp, nil, err)
		}
	}
}
