package marketplace

import (
	"context"
	"fmt"
	"reflect"
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

func CreateOrUpdateSubscription(ctx context.Context, client runtimeclient.Client, extensionName string, subscription Subscription) error {
	storedSubscription := &marketplacev1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", extensionName, subscription.SubscriptionID),
		},
	}

	mutateFn := func() error {
		storedSubscription.Labels = map[string]string{
			marketplacev1alpha1.ExtensionID:      subscription.ExtensionID,
			corev1alpha1.ExtensionReferenceLabel: extensionName,
		}
		storedSubscription.SubscriptionSpec.ExtensionName = extensionName
		storedSubscription.SubscriptionStatus.ExpiredAt = parseTime(subscription.ExpiredAt)
		storedSubscription.SubscriptionStatus.StartedAt = parseTime(subscription.StartedAt)
		storedSubscription.SubscriptionStatus.CreatedAt = parseTime(subscription.CreatedAt)
		storedSubscription.SubscriptionStatus.ExtensionID = subscription.ExtensionID
		storedSubscription.SubscriptionStatus.ExtraInfo = subscription.ExtraInfo
		storedSubscription.SubscriptionStatus.OrderID = subscription.OrderID
		storedSubscription.SubscriptionStatus.UpdatedAt = parseTime(subscription.UpdatedAt)
		storedSubscription.SubscriptionStatus.UserID = subscription.UserID
		storedSubscription.SubscriptionStatus.SubscriptionID = subscription.SubscriptionID
		storedSubscription.SubscriptionStatus.UserSubscriptionID = subscription.UserSubscriptionID
		return nil
	}
	op, err := controllerutil.CreateOrUpdate(ctx, client, storedSubscription, mutateFn)
	if err != nil {
		return err
	}
	klog.Infof("Subscription %s has been %s", storedSubscription.Name, op)
	return nil
}

func RemoveSubscriptions(ctx context.Context, client runtimeclient.Client, extension *corev1alpha1.Extension) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := client.Get(ctx, types.NamespacedName{Name: extension.Name}, extension); err != nil {
			return err
		}
		expected := extension.DeepCopy()
		delete(expected.Labels, marketplacev1alpha1.Subscribed)
		if !reflect.DeepEqual(expected.Labels, extension.Labels) {
			return client.Update(ctx, expected)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to update extension: %s", err)
	}
	return client.DeleteAllOf(ctx, &marketplacev1alpha1.Subscription{}, runtimeclient.MatchingLabels{corev1alpha1.ExtensionReferenceLabel: extension.Name})
}
