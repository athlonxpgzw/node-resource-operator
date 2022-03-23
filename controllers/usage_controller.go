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

// PodMetric contains pod metric value (the metric values are expected to be the metric as a milli-value)
type NodeMetric struct {
	Timestamp time.Time
	Window    time.Duration
	Value     int64
}

// PodMetricsInfo contains pod metrics as a map from pod names to PodMetricsInfo
type NodeMetricsInfo map[string]NodeMetric

const (
	metricServerDefaultMetricWindow = time.Minute
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
	log.V(1).Info("usage", "usage name", usages.Name, "Node count", usages.Status.NodeCount, "LastMetricTime", usages.Status.LastMetricTimestamp)

	var originNodes *corev1.NodeList
	var err error
	// if err := r.List(ctx, &originNodes); err != nil {
	// 	log.Error(err, "unable to list child Jobs")
	// 	return ctrl.Result{}, err
	// }
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

	var timestamp metav1.Time
	for _, metric := range usages.Spec.Metrics {
		if metric.Type == cloudcmftcomv1alpha1.NodesMetricSourceType {
			metrics, err := r.metricsClient.RootScopedMetrics().GetForObjects(schema.GroupKind{Kind: "Node"}, labels.Everything(), metric.Name, labels.Everything())
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("unable to fetch metrics from custom metrics API: %v", err)
			}

			if len(metrics.Items) == 0 {
				return ctrl.Result{}, fmt.Errorf("no metrics returned from custom metrics API")
			}
			//res := make(NodeMetricsInfo, len(metrics.Items))
			for _, m := range metrics.Items {
				// window := metricServerDefaultMetricWindow
				// if m.WindowSeconds != nil {
				// 	window = time.Duration(*m.WindowSeconds) * time.Second
				// }
				// res[m.DescribedObject.Name] = NodeMetric{
				// 	Timestamp: m.Timestamp.Time,
				// 	Window:    window,
				// 	Value:     int64(m.Value.MilliValue()),
				// }
				if nodeAnnotation, ok := nodeAnnotations[m.DescribedObject.Name]; !ok {
					continue
				} else {
					nodeAnnotation[metric.Name] = m.Timestamp.Format(time.RFC3339) + "--" + m.Value.String()
				}
				timestamp = metrics.Items[0].Timestamp
			}
		}
	}

	for _, node := range nodes.Items {
		for key, value := range nodeAnnotations[node.Name] {
			node.Annotations[key] = value
		}
		// if err := r.Update(ctx, &node); err != nil {
		// 	log.Error(err, fmt.Sprintf("Unable to annotate the node: %s", node.Name) )
		// }
		if _, err := r.k8sClient.CoreV1().Nodes().Update(ctx, &node, metav1.UpdateOptions{}); err != nil {
			log.Error(err, fmt.Sprintf("Unable to annotate the node: %s", node.Name))
		}
	}

	usages.Status.NodeCount = len(nodes.Items)
	usages.Status.LastMetricTimestamp = timestamp

	if err := r.Status().Update(ctx, &usages); err != nil {
		log.Error(err, "unable to update Usage status")
		return ctrl.Result{}, err
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
