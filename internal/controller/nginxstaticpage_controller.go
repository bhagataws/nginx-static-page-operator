package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	staticv1 "github.com/bhagataws/nginx-static-page-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// NginxStaticPageReconciler reconciles a NginxStaticPage object
type NginxStaticPageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const finalizerName = "static.cafeco.io/finalizer"

// +kubebuilder:rbac:groups=static.cafeco.io,resources=nginxstaticpages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=static.cafeco.io,resources=nginxstaticpages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=static.cafeco.io,resources=nginxstaticpages/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

func (r *NginxStaticPageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var page staticv1.NginxStaticPage
	if err := r.Get(ctx, req.NamespacedName, &page); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	replicas := int32(1)
	if page.Spec.ReplicaCount != nil {
		replicas = *page.Spec.ReplicaCount
	}

	configMapName := fmt.Sprintf("%s-content", page.Name)
	labels := map[string]string{
		"app": page.Name,
	}

	content := page.Spec.StaticContent
	if content == "" {
		content = "<html><body><h1>Welcome</h1></body></html>"
	}

	desiredConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: page.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{
			"index.html": content,
		},
	}

	if err := ctrl.SetControllerReference(&page, desiredConfigMap, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	var existingConfigMap corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: page.Namespace}, &existingConfigMap)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, desiredConfigMap); err != nil {
				log.Error(err, "Failed to create ConfigMap", "name", desiredConfigMap.Name)
				return ctrl.Result{}, err
			}
			log.Info("Created ConfigMap", "name", desiredConfigMap.Name)
		} else {
			return ctrl.Result{}, err
		}
	} else {
		existingConfigMap.Data = desiredConfigMap.Data
		existingConfigMap.Labels = desiredConfigMap.Labels

		if err := r.Update(ctx, &existingConfigMap); err != nil {
			log.Error(err, "Failed to update ConfigMap", "name", existingConfigMap.Name)
			return ctrl.Result{}, err
		}
		log.Info("Updated ConfigMap", "name", existingConfigMap.Name)
	}

	desiredDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      page.Name,
			Namespace: page.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:stable",
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: 80,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "page-content",
									MountPath: "/usr/share/nginx/html/index.html",
									SubPath:   "index.html",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "page-content",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(&page, desiredDeployment, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	var existingDeployment appsv1.Deployment
	err = r.Get(ctx, types.NamespacedName{Name: page.Name, Namespace: page.Namespace}, &existingDeployment)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, desiredDeployment); err != nil {
				log.Error(err, "Failed to create Deployment", "name", desiredDeployment.Name)
				return ctrl.Result{}, err
			}
			log.Info("Created Deployment", "name", desiredDeployment.Name)
		} else {
			return ctrl.Result{}, err
		}
	} else {
		existingDeployment.Labels = desiredDeployment.Labels
		existingDeployment.Spec.Replicas = desiredDeployment.Spec.Replicas
		existingDeployment.Spec.Selector = desiredDeployment.Spec.Selector
		existingDeployment.Spec.Template = desiredDeployment.Spec.Template

		if err := r.Update(ctx, &existingDeployment); err != nil {
			log.Error(err, "Failed to update Deployment", "name", existingDeployment.Name)
			return ctrl.Result{}, err
		}
		if !page.DeletionTimestamp.IsZero() {
			log.Info("Deleting NginxStaticPage", "name", page.Name, "namespace", page.Namespace)
			// do cleanup if needed
			controllerutil.RemoveFinalizer(&page, finalizerName)
			if err := r.Update(ctx, &page); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if !controllerutil.ContainsFinalizer(&page, finalizerName) {
			controllerutil.AddFinalizer(&page, finalizerName)
			if err := r.Update(ctx, &page); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		log.Info("Updated Deployment", "name", existingDeployment.Name)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NginxStaticPageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&staticv1.NginxStaticPage{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Named("nginxstaticpage").
		Complete(r)
}
