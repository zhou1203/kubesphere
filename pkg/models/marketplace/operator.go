package marketplace

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	corev1alpha1 "kubesphere.io/api/core/v1alpha1"
	marketplacev1alpha1 "kubesphere.io/api/marketplace/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func parseTime(timeString string) *metav1.Time {
	empty := metav1.NewTime(time.Time{})
	if timeString == "" {
		return &empty
	}
	parsedTime, err := time.Parse(time.RFC3339, timeString)
	if err != nil {
		return &empty
	}
	Time := metav1.NewTime(parsedTime)
	return &Time
}

func CreateOrUpdateSubscription(ctx context.Context, client runtimeclient.Client, extension *corev1alpha1.Extension, subscription Subscription) error {
	subCR := &marketplacev1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", extension.Name, subscription.SubscriptionID),
		},
	}

	mutateFn := func() error {
		subCR.Labels = map[string]string{
			marketplacev1alpha1.ExtensionID:      subscription.ExtensionID,
			corev1alpha1.ExtensionReferenceLabel: extension.Name,
		}
		subCR.SubscriptionSpec.ExtensionName = extension.Name
		subCR.SubscriptionStatus.ExpiredAt = parseTime(subscription.ExpiredAt)
		subCR.SubscriptionStatus.StartedAt = parseTime(subscription.StartedAt)
		subCR.SubscriptionStatus.CreatedAt = parseTime(subscription.CreatedAt)
		subCR.SubscriptionStatus.ExtensionID = subscription.ExtensionID
		subCR.SubscriptionStatus.ExtraInfo = subscription.ExtraInfo
		subCR.SubscriptionStatus.OrderID = subscription.OrderID
		subCR.SubscriptionStatus.UpdatedAt = parseTime(subscription.UpdatedAt)
		subCR.SubscriptionStatus.UserID = subscription.UserID
		subCR.SubscriptionStatus.SubscriptionID = subscription.SubscriptionID
		subCR.SubscriptionStatus.UserSubscriptionID = subscription.UserSubscriptionID
		return nil
	}
	op, err := controllerutil.CreateOrUpdate(ctx, client, subCR, mutateFn)
	if err != nil {
		return err
	}
	klog.Infof("Subscription %s has been %s", subCR.Name, op)
	return nil
}

func RemoveSubscription(ctx context.Context, client runtimeclient.Client, extension *corev1alpha1.Extension) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := client.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return err
		}
		extension = extension.DeepCopy()
		if extension.Status.State == "" {
			delete(extension.Labels, marketplacev1alpha1.Subscribed)
		} else if extension.Labels[marketplacev1alpha1.Subscribed] == "true" {
			extension.Labels[marketplacev1alpha1.Subscribed] = "false"
		}
		return client.Update(ctx, extension)
	})
	if err != nil {
		return err
	}
	return client.DeleteAllOf(ctx, &marketplacev1alpha1.Subscription{}, runtimeclient.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: extension.Name})
}
