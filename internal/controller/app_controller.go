/*
Copyright 2023.

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

package controller

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	kv2 "k8s.io/api/apps/v1"
	kv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	_ "kube-operator-demo/api/v1"
	appsv1 "kube-operator-demo/api/v1"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"time"
)

const appFinalizer = "apps.kubeoperator.local/finalizer"

// Definitions to manage status conditions
const (
	// typeAvailableApp represents the status of the Deployment reconciliation
	typeAvailableApp = "Available"
	// typeDegradedApp represents the status used when the custom resource is deleted and the finalizer operations are must to occur.
	typeDegradedApp = "Degraded"
)

// AppReconciler reconciles a App object
type AppReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// The following markers are used to generate the rules permissions (RBAC) on config/rbac using controller-gen
// when the command <make manifests> is executed.
// To know more about markers see: https://book.kubebuilder.io/reference/markers.html

// +kubebuilder:rbac:groups=apps.kubeoperator.local,resources=apps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.kubeoperator.local,resources=apps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.kubeoperator.local,resources=apps/finalizers,verbs=update
// +kubebuilder:rbac:groups=api.apps.v1,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=api.core.v1,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=api.apps.v1,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=api.autoscaling.v2beta1,resources=hpa,verbs=get;list;watch;create;update;patch;delete

// Reconcile It is essential for the controller's reconciliation loop to be idempotent. By following the Operator
// pattern you will create Controllers which provide a reconcile function
// responsible for synchronizing resources until the desired state is reached on the cluster.
// Breaking this recommendation goes against the design principles of controller-runtime.
// and may lead to unforeseen consequences such as resources becoming stuck and requiring manual intervention.
// For further info:
// - About Operator Pattern: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
// - About Controllers: https://kubernetes.io/docs/concepts/architecture/controller/
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *AppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)
	nginxApp := &appsv1.App{}

	// Initial check; status, finalizer
	if result, err, failed := checkStatus(ctx, req, r, nginxApp, l); failed {
		return result, err
	}

	// Finalizer removal if needed
	if result, err, failed := r.finalizerDeletion(ctx, req, nginxApp, l); failed {
		return result, err
	}

	// Check if the deployment already exists, if not create a new one
	found := &kv2.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: nginxApp.Name, Namespace: nginxApp.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		dep, err := r.deploymentForNginxApp(req, nginxApp, l)
		if err != nil {
			l.Error(err, "Failed to define new Deployment resource for NginxApp")

			// The following implementation will update the status
			meta.SetStatusCondition(
				&nginxApp.Status.Conditions, metav1.Condition{
					Type:   typeAvailableApp,
					Status: metav1.ConditionFalse,
					Reason: "Reconciling",
					Message: fmt.Sprintf(
						"Failed to create Deployment for the custom resource (%s): (%s)",
						nginxApp.Name,
						err,
					),
				},
			)

			if err := r.Status().Update(ctx, nginxApp); err != nil {
				l.Error(err, "Failed to update NginxApp status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		l.Info("Creating a new Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			l.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		l.Error(err, "Failed to get Deployment")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// The CRD API is defining that the NginxApp type, have a NginxAppSpec.Size field
	// to set the quantity of Deployment instances is the desired state on the cluster.
	// Therefore, the following code will ensure the Deployment size is the same as defined
	// via the Size spec of the Custom Resource which we are reconciling.
	size := nginxApp.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		if err = r.Update(ctx, found); err != nil {
			l.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			// Re-fetch the nginxApp Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, nginxApp); err != nil {
				l.Error(err, "Failed to re-fetch nginxApp")
				return ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&nginxApp.Status.Conditions, metav1.Condition{Type: typeAvailableApp,
				Status: metav1.ConditionFalse, Reason: "Resizing",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", nginxApp.Name, err)})

			if err := r.Status().Update(ctx, nginxApp); err != nil {
				l.Error(err, "Failed to update NginxApp status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{Requeue: true}, nil
	}

	// The following implementation will update the status
	meta.SetStatusCondition(&nginxApp.Status.Conditions, metav1.Condition{Type: typeAvailableApp,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("Deployment for custom resource (%s) with %d replicas created successfully", nginxApp.Name, size)})

	if err := r.Status().Update(ctx, nginxApp); err != nil {
		l.Error(err, "Failed to update NginxApp status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// finalizerDeletion
func (r *AppReconciler) finalizerDeletion(
	ctx context.Context,
	req ctrl.Request,
	nginxApp *appsv1.App,
	l logr.Logger) (ctrl.Result, error, bool) {
	// Check if the NginxApp instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isNginxAppMarkedToBeDeleted := nginxApp.GetDeletionTimestamp() != nil
	if isNginxAppMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(nginxApp, appFinalizer) {
			l.Info("Performing Finalizer Operations for NginxApp before delete CR")

			// Let's add here an status "Downgrade" to define that this resource begin its process to be terminated.
			meta.SetStatusCondition(&nginxApp.Status.Conditions, metav1.Condition{Type: typeDegradedApp,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", nginxApp.Name)})

			if err := r.Status().Update(ctx, nginxApp); err != nil {
				l.Error(err, "Failed to update NginxApp status")
				return ctrl.Result{}, err, true
			}

			// Perform all operations required before remove the finalizer and allow
			// the Kubernetes API to remove the custom resource.
			r.doFinalizerOperationsForNginxApp(nginxApp)

			if err := r.Get(ctx, req.NamespacedName, nginxApp); err != nil {
				l.Error(err, "Failed to re-fetch nginxApp")
				return ctrl.Result{}, err, true
			}

			meta.SetStatusCondition(&nginxApp.Status.Conditions, metav1.Condition{Type: typeDegradedApp,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", nginxApp.Name)})

			if err := r.Status().Update(ctx, nginxApp); err != nil {
				l.Error(err, "Failed to update NginxApp status")
				return ctrl.Result{}, err, true
			}

			l.Info("Removing Finalizer for NginxApp after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(nginxApp, appFinalizer); !ok {
				l.Error(nil, "Failed to remove finalizer for NginxApp")
				return ctrl.Result{Requeue: true}, nil, true
			}

			if err := r.Update(ctx, nginxApp); err != nil {
				l.Error(err, "Failed to remove finalizer for NginxApp")
				return ctrl.Result{}, err, true
			}
		}
		return ctrl.Result{}, nil, true
	}
	return ctrl.Result{}, nil, false
}

func checkStatus(
	ctx context.Context,
	req ctrl.Request,
	r *AppReconciler,
	nginxApp *appsv1.App,
	l logr.Logger) (ctrl.Result, error, bool) {
	err := r.Get(ctx, req.NamespacedName, nginxApp)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			l.Info("nginxApp resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, err, true
		}
		// Error reading the object - requeue the request.
		l.Error(err, "Failed to get nginxApp")
		return ctrl.Result{}, err, true
	}

	// Let's just set the status as Unknown when no status are available
	if nginxApp.Status.Conditions == nil || len(nginxApp.Status.Conditions) == 0 {
		meta.SetStatusCondition(
			&nginxApp.Status.Conditions,
			metav1.Condition{
				Type:    typeAvailableApp,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			},
		)
		if err = r.Status().Update(ctx, nginxApp); err != nil {
			l.Error(err, "Failed to update NginxApp status")
			return ctrl.Result{}, err, true
		}

		// Let's re-fetch the nginxApp Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster
		if err := r.Get(ctx, req.NamespacedName, nginxApp); err != nil {
			l.Error(err, "Failed to re-fetch nginxApp")
			return ctrl.Result{}, err, true
		}
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occurs before the custom resource to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(nginxApp, appFinalizer) {
		l.Info("No Finalizers found, creating it for " + nginxApp.Name)
		if ok := controllerutil.AddFinalizer(nginxApp, appFinalizer); !ok {
			l.Error(err, "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil, true
		}

		if err = r.Update(ctx, nginxApp); err != nil {
			l.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err, true
		}
	}

	return ctrl.Result{}, nil, false
}

// doFinalizerOperationsForNginxApp will perform the required operations before delete the CR.
func (r *AppReconciler) doFinalizerOperationsForNginxApp(cr *appsv1.App) {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.

	// Note: It is not recommended to use finalizers with the purpose of delete resources which are
	// created and managed in the reconciliation. These ones, such as the Deployment created on this reconcile,
	// are defined as depended of the custom resource. See that we use the method ctrl.SetControllerReference.
	// to set the ownerRef which means that the Deployment will be deleted by the Kubernetes API.
	// More info: https://kubernetes.io/docs/tasks/administer-cluster/use-cascading-deletion/

	// The following implementation will raise an event
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s",
			cr.Name,
			cr.Namespace))
}

// deploymentForNginxApp returns a NginxApp Deployment object
func (r *AppReconciler) deploymentForNginxApp(
	req ctrl.Request,
	nginxApp *appsv1.App,
	l logr.Logger) (*kv2.Deployment, error) {
	l.Info(fmt.Sprintf("[Creating new deployment: %s]", req.Name))

	// Get the image name from env, otherwise from cfg
	image, err := imageForNginxApp()
	if err != nil {
		image = nginxApp.Spec.Image
	}
	_ = labelsForNginxApp(nginxApp.Name)

	dep := &kv2.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    nginxApp.ObjectMeta.Labels,
			Name:      req.Name,
			Namespace: req.Namespace,
		},
		Spec: kv2.DeploymentSpec{
			Replicas: &nginxApp.Spec.Size,
			Selector: &metav1.LabelSelector{
				MatchLabels: nginxApp.ObjectMeta.Labels, // or ls to make it more secure
			},
			Template: kv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:    nginxApp.ObjectMeta.Labels, // or ls to make it more secure
					Name:      req.Name,
					Namespace: req.Namespace,
				},
				Spec: kv1.PodSpec{
					SecurityContext: &kv1.PodSecurityContext{
						// IMPORTANT: seccomProfile require Kubernetes 1.19>=
						SeccompProfile: &kv1.SeccompProfile{
							Type: kv1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []kv1.Container{{
						Name:  nginxApp.Name,
						Image: image,
						Ports: []kv1.ContainerPort{
							{
								ContainerPort: nginxApp.Spec.Port,
							},
						},
						ImagePullPolicy: kv1.PullIfNotPresent,
					}},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(nginxApp, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// labelsForNginxApp returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForNginxApp(name string) map[string]string {
	var imageTag string
	image, err := imageForNginxApp()
	if err == nil {
		imageTag = strings.Split(image, ":")[1]
	}
	return map[string]string{"app.kubernetes.io/name": "app-kubernetes-operator-demo",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/version":    imageTag,
		"app.kubernetes.io/part-of":    "kube-operator-demo",
		"app.kubernetes.io/created-by": "controller-manager",
	}
}

// imageForNginxApp gets the Operand image which is managed by this controller
// from the APP_IMAGE environment variable defined in the config/manager/manager.yaml
func imageForNginxApp() (string, error) {
	var imageEnvVar = "APP_IMAGE"
	image, found := os.LookupEnv(imageEnvVar)
	if !found {
		return "", fmt.Errorf("Unable to find %s environment variable with the image", imageEnvVar)
	}
	return image, nil
}

// SetupWithManager sets up the controller with the Manager.
// Note that the Deployment will be also watched in order to ensure its
// desirable state on the cluster
func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.App{}).
		Owns(&kv2.Deployment{}).
		Complete(r)
}
