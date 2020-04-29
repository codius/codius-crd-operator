/*


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
	"os"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	codiusv1 "codius-crd-operator/api/v1"
)

// ServiceReconciler reconciles a Service object
type ServiceReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core.codius.org,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.codius.org,resources=services/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;patch;create;update
// +kubebuilder:rbac:groups=core,resources=services,verbs=list;watch;get;patch;create;update
func (r *ServiceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("service", req.NamespacedName)

	// your logic here
	var codiusService codiusv1.Service
	if err := r.Get(ctx, req.NamespacedName, &codiusService); err != nil {
		log.Error(err, "unable to fetch Codius Service")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the deployment already exists, if not create a new one
	var deployment appsv1.Deployment
	err := r.Get(ctx, req.NamespacedName, &deployment)
	if err != nil && errors.IsNotFound(err) {
		dep := deploymentForCR(&codiusService)
		// Set Codius Service as the owner and controller
		if err := controllerutil.SetControllerReference(&codiusService, dep, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Client.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully - don't requeue
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Check if the Service already exists, if not create a new one
	var service corev1.Service
	err = r.Get(ctx, req.NamespacedName, &service)
	if err != nil && errors.IsNotFound(err) {
		ser := serviceForCR(&codiusService)
		// Set Codius Service instance as the owner and controller
		if err := controllerutil.SetControllerReference(&codiusService, ser, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Creating a new Service.", "Service.Namespace", ser.Namespace, "Service.Name", ser.Name)
		err = r.Client.Create(ctx, ser)
		if err != nil {
			log.Error(err, "Failed to create new Service.", "Service.Namespace", ser.Namespace, "Service.Name", ser.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Service.")
		return ctrl.Result{}, err
	}

	// Deployment and Service already exists - don't requeue
	log.Info("Skip reconcile: Deployment and Service already exist", "Namespace", deployment.Namespace, "Deployment.Name", deployment.Name, "Service.Name", service.Name)
	return ctrl.Result{}, nil
}

func deploymentForCR(cr *codiusv1.Service) *appsv1.Deployment {
	labels := labelsForCR(cr)
	containers := make([]corev1.Container, len(cr.Spec.Containers))
	for i, container := range cr.Spec.Containers {
		envVars := make([]corev1.EnvVar, len(container.Env))
		for j, env := range container.Env {
			var valueFrom *corev1.EnvVarSource
			if env.ValueFrom != nil {
				valueFrom = &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: env.ValueFrom.SecretKeyRef.Name,
						},
						Key: env.ValueFrom.SecretKeyRef.Key,
					},
				}
			}
			envVars[j] = corev1.EnvVar{
				Name:      env.Name,
				Value:     env.Value,
				ValueFrom: valueFrom,
			}
		}
		containers[i] = corev1.Container{
			Name:       container.Name,
			Image:      container.Image,
			Command:    container.Command,
			Args:       container.Args,
			WorkingDir: container.WorkingDir,
			Env:        envVars,
		}
	}

	automountServiceAccountToken := false

	var pRuntimeClassName *string
	runtimeClassName := os.Getenv("RUNTIME_CLASS_NAME")
	if runtimeClassName != "" {
		pRuntimeClassName = &runtimeClassName
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			// Replicas: &replicas,   // Default to 1
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers:                   containers,
					DNSPolicy:                    corev1.DNSDefault,
					AutomountServiceAccountToken: &automountServiceAccountToken,
					RuntimeClassName:             pRuntimeClassName,
				},
			},
		},
	}
}

func serviceForCR(cr *codiusv1.Service) *corev1.Service {
	labels := labelsForCR(cr)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Port: cr.Spec.Port,
				},
			},
		},
	}
}

// labelsForCR returns the labels for selecting the resources
// belonging to the given Codius Service name.
func labelsForCR(cr *codiusv1.Service) map[string]string {
	return map[string]string{"app": cr.Name}
}

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&codiusv1.Service{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}
