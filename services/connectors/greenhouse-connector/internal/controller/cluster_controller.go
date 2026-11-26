package controller

import (
	"context"

	greenhousev1alpha1 "github.com/cloudoperators/greenhouse/api/v1alpha1"
	"github.com/operational-context-graph/operational-context-graph/services/connectors/greenhouse-connector/internal/publisher"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ClusterReconciler reconciles Cluster objects
type ClusterReconciler struct {
	client.Client
	Publisher *publisher.Publisher
}

// +kubebuilder:rbac:groups=greenhouse.sap,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=greenhouse.sap,resources=clusters/status,verbs=get

func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cluster greenhousev1alpha1.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Cluster not found, skipping", "name", req.Name, "namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("reconciling Cluster",
		"name", cluster.Name,
		"namespace", cluster.Namespace,
		"accessMode", cluster.Spec.AccessMode,
		"kubernetesVersion", cluster.Status.KubernetesVersion,
	)

	if err := r.Publisher.Publish(ctx, "greenhouse.Cluster", "0.1.0", cluster.Namespace, cluster.Name, &cluster); err != nil {
		log.Error(err, "failed to publish Cluster")
		return ctrl.Result{}, err
	}

	log.Info("published Cluster", "name", cluster.Name, "namespace", cluster.Namespace)
	return ctrl.Result{}, nil
}

func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&greenhousev1alpha1.Cluster{}).
		Named("cluster").
		Complete(r)
}
