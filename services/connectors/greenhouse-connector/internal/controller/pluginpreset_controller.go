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

// PluginPresetReconciler reconciles PluginPreset objects
type PluginPresetReconciler struct {
	client.Client
	Publisher *publisher.Publisher
}

// +kubebuilder:rbac:groups=greenhouse.sap,resources=pluginpresets,verbs=get;list;watch
// +kubebuilder:rbac:groups=greenhouse.sap,resources=pluginpresets/status,verbs=get

func (r *PluginPresetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var pp greenhousev1alpha1.PluginPreset
	if err := r.Get(ctx, req.NamespacedName, &pp); err != nil {
		if errors.IsNotFound(err) {
			log.Info("PluginPreset not found, skipping", "name", req.Name, "namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("reconciling PluginPreset",
		"name", pp.Name,
		"namespace", pp.Namespace,
		"totalPlugins", pp.Status.TotalPlugins,
		"readyPlugins", pp.Status.ReadyPlugins,
		"failedPlugins", pp.Status.FailedPlugins,
	)

	if err := r.Publisher.Publish(ctx, "greenhouse.PluginPreset", "0.1.0", pp.Namespace, pp.Name, &pp); err != nil {
		log.Error(err, "failed to publish PluginPreset")
		return ctrl.Result{}, err
	}

	log.Info("published PluginPreset", "name", pp.Name, "namespace", pp.Namespace)
	return ctrl.Result{}, nil
}

func (r *PluginPresetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&greenhousev1alpha1.PluginPreset{}).
		Named("pluginpreset").
		Complete(r)
}
