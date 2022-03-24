/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	cacheddiscovery "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/kubernetes"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
	"k8s.io/metrics/pkg/client/custom_metrics"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cloudcmftcomv1alpha1 "github.com/athlonxpgzw/node-resource-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UsageReconciler reconciles a Usage object
type UsageReconciler struct {
	client.Client
	k8sClient      kubernetes.Interface
	Scheme         *runtime.Scheme
	NodeSyncPeriod time.Duration
	metricsClient  custom_metrics.CustomMetricsClient
}

const (
	annotationPrefix = "NodeUsage/"
	finalizer        = "finalizer.cmft.com/usages"
)

//+kubebuilder:rbac:groups=cloud.cmft.com,resources=usages,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cloud.cmft.com,resources=usages/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cloud.cmft.com,resources=usages/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Usage object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *UsageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var usages cloudcmftcomv1alpha1.Usage
	if err := r.Get(ctx, req.NamespacedName, &usages); err != nil {
		log.Error(err, "unable to fetch Usages")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.V(1).Info("usage", "usage name", usages.Name, "Node count", usages.Status.NodeCount)

	if usages.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(&usages, finalizer) {
			controllerutil.AddFinalizer(&usages, finalizer)
			if err := r.Update(ctx, &usages); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(&usages, finalizer) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteNodeAnnotations(ctx, &usages); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(&usages, finalizer)
			if err := r.Update(ctx, &usages); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	var originNodes *corev1.NodeList
	var err error

	if originNodes, err = r.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err != nil {
		log.Error(err, "unable to list Nodes")
		return ctrl.Result{}, err
	}
	log.V(1).Info("nodes", "fetched nodes", len(originNodes.Items))

	var nodes = originNodes.DeepCopy()

	var nodeAnnotations map[string]map[string]string = make(map[string]map[string]string)
	for _, node := range nodes.Items {
		nodeAnnotations[node.Name] = make(map[string]string)
	}

	for _, metric := range usages.Spec.Metrics {
		if metric.Type == cloudcmftcomv1alpha1.NodesMetricSourceType {
			metrics, err := r.metricsClient.RootScopedMetrics().GetForObjects(schema.GroupKind{Kind: "Node"}, labels.Everything(), metric.Name, labels.Everything())
			if err != nil {
				//return ctrl.Result{}, fmt.Errorf("unable to fetch metrics from custom metrics API: %v", err)
				log.V(1).Info(fmt.Sprintf("unable to fetch metrics from custom metrics API: %v", err))
				continue
			}

			if len(metrics.Items) == 0 {
				return ctrl.Result{}, fmt.Errorf("no metrics returned from custom metrics API")
			}

			for _, m := range metrics.Items {

				if nodeAnnotation, ok := nodeAnnotations[m.DescribedObject.Name]; !ok {
					continue
				} else {
					nodeAnnotation[annotationPrefix+metric.Name] = m.Timestamp.Format(time.RFC3339) + "@" + m.Value.String()
				}
			}
		}
	}

	for _, node := range nodes.Items {
		for key, value := range nodeAnnotations[node.Name] {
			node.Annotations[key] = value
		}
		if _, err := r.k8sClient.CoreV1().Nodes().Update(ctx, &node, metav1.UpdateOptions{}); err != nil {
			log.Error(err, fmt.Sprintf("Unable to annotate the node: %s", node.Name))
		}
	}

	if usages.Status.NodeCount != len(nodes.Items) {
		usages.Status.NodeCount = len(nodes.Items)
		if err := r.Status().Update(ctx, &usages); err != nil {
			log.Error(err, "unable to update Usage status")
			return ctrl.Result{}, err
		}
	}

	scheduledResult := ctrl.Result{RequeueAfter: r.NodeSyncPeriod}

	return scheduledResult, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UsageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.TODO()
	clientConfig := mgr.GetConfig()
	client, err := clientset.NewForConfig(clientConfig)
	if err != nil {
		return err
	}
	r.k8sClient = client

	cachedClient := cacheddiscovery.NewMemCacheClient(client.Discovery())
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedClient)
	go wait.Until(func() {
		restMapper.Reset()
	}, 30*time.Second, ctx.Done())

	apiVersionsGetter := custom_metrics.NewAvailableAPIsGetter(client.Discovery())
	// invalidate the discovery information roughly once per resync interval our API
	// information is *at most* two resync intervals old.
	go custom_metrics.PeriodicallyInvalidate(
		apiVersionsGetter,
		r.NodeSyncPeriod,
		ctx.Done())

	r.metricsClient = custom_metrics.NewForConfig(clientConfig, restMapper, apiVersionsGetter)

	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudcmftcomv1alpha1.Usage{}).
		Complete(r)
}

func (r *UsageReconciler) deleteNodeAnnotations(ctx context.Context, usages *cloudcmftcomv1alpha1.Usage) error {
	log := log.FromContext(ctx)
	var originNodes *corev1.NodeList
	var err error

	if originNodes, err = r.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err != nil {
		log.Error(err, "unable to list Nodes")
		return err
	}

	nodes := originNodes.DeepCopy()
	for _, node := range nodes.Items {
		for _, metric := range usages.Spec.Metrics {
			delete(node.Annotations, annotationPrefix+metric.Name)
		}
		if _, err := r.k8sClient.CoreV1().Nodes().Update(ctx, &node, metav1.UpdateOptions{}); err != nil {
			log.Error(err, fmt.Sprintf("Unable to annotate the node: %s", node.Name))
			return err
		}
	}
	return nil
}
