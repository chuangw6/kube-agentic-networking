package controller

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions/apis/v1"
)

func (c *Controller) setupGatewayClassEventHandlers(gatewayClassInformer gatewayinformers.GatewayClassInformer) error {
	_, err := gatewayClassInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				c.syncGatewayClass(key)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(newObj)
			if err == nil {
				c.syncGatewayClass(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				c.syncGatewayClass(key)
			}
		},
	})
	return err
}

func (c *Controller) syncGatewayClass(key string) {
	startTime := time.Now()
	klog.V(2).Infof("Started syncing gatewayclass %q (%v)", key, time.Since(startTime))
	defer func() {
		klog.V(2).Infof("Finished syncing gatewayclass %q (%v)", key, time.Since(startTime))
	}()

	gwc, err := c.gatewayClassLister.Get(key)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.InfoS("GatewayClass deleted", "gatewayclass", key)
		}
		return
	}

	// We only care about the GatewayClass that matches our controller name.
	if gwc.Spec.ControllerName != controllerName {
		return
	}

	newGwc := gwc.DeepCopy()
	// Set the "Accepted" condition to True and update the observedGeneration.
	meta.SetStatusCondition(&newGwc.Status.Conditions, metav1.Condition{
		Type:               string(gatewayv1.GatewayClassConditionStatusAccepted),
		Status:             metav1.ConditionTrue,
		Reason:             string(gatewayv1.GatewayClassReasonAccepted),
		Message:            "GatewayClass is accepted by this controller.",
		ObservedGeneration: gwc.Generation,
	})

	// Update the status on the API server.
	if _, err := c.gwClient.GatewayV1().GatewayClasses().UpdateStatus(context.Background(), newGwc, metav1.UpdateOptions{}); err != nil {
		klog.Errorf("failed to update gatewayclass status: %v", err)
	} else {
		klog.InfoS("GatewayClass status updated", "gatewayclass", key)
	}
}
