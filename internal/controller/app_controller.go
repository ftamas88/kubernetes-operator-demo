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
	corev1 "k8s.io/api/core/v1"
	kv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	_ "kube-operator-demo/api/v1"
	appsv1 "kube-operator-demo/api/v1"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

//||OLD||\\kubebuilder:rbac:groups=apps.kubeoperator.local,resources=apps,verbs=get;list;watch;create;update;patch;delete
//||OLD||\\kubebuilder:rbac:groups=apps.kubeoperator.local,resources=apps/status,verbs=get;update;patch
//||OLD||\\kubebuilder:rbac:groups=apps.kubeoperator.local,resources=apps/finalizers,verbs=update
//||OLD||\\kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//||OLD||\\kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//||OLD||\\kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

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

	// If any changes occur then reconcile function will be called.
	// Get the app object on which reconcile is called
	var app appsv1.App
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		l.Info("Unable to fetch the App", "Error", err)

		// Delete Deployment if it exists
		var appDeployment kv2.Deployment
		if err := r.Get(ctx, req.NamespacedName, &appDeployment); err == nil {
			return r.RemoveDeployment(ctx, &appDeployment, l)
		}

		// Delete service if it exists
		var appService kv1.Service
		if err := r.Get(ctx, req.NamespacedName, &appService); err == nil {
			return r.RemoveService(ctx, &appService, l)
		}

		// Delete HPA if it exists
		/* DEPRECATED
		var appHPA kv3.HorizontalPodAutoscaler
		if err := r.Get(ctx, req.NamespacedName, &appHPA); err == nil {
			return r.RemoveHPA(ctx, &appHPA, l)
		}*/

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If we have the website resource we need to ensure that the child resources are created as well.
	l.Info("Ensuring Deployment is created", "App", req.NamespacedName)
	var appDeployment kv2.Deployment
	if err := r.Get(ctx, req.NamespacedName, &appDeployment); err != nil {
		l.Info("unable to fetch Deployment for app", "App", req.NamespacedName)
		// Create a deployment
		return r.CreateDeployment(ctx, req, app, l)
	}
	// Ensure that at least minimum number of replicas are maintained
	if *(appDeployment.Spec.Replicas) < *(app.Spec.MinReplica) {
		// Update the deployment with the required replica
		appDeployment.Spec.Replicas = app.Spec.MinReplica
		if err := r.Update(ctx, &appDeployment); err != nil {
			l.Error(err, "unable to update the deployment for app", "App", req.NamespacedName)
			return ctrl.Result{}, err
		}
	}

	// Ensure that the service is created for the website
	l.Info("Ensuring Service is created", "App", req.NamespacedName)
	var websiteService kv1.Service
	if err := r.Get(ctx, req.NamespacedName, &websiteService); err != nil {
		l.Info("unable to fetch Deployment for app", "App", req.NamespacedName)
		// Create the service
		return r.CreateService(ctx, req, app, l)
	}

	// Ensure that the service is created for the website
	// Deprecated..
	/*
		l.Info("Ensuring HPA is created", "Website", req.NamespacedName)
		var websitesHPA kv3.HorizontalPodAutoscaler
		if err := r.Get(ctx, req.NamespacedName, &websitesHPA); err != nil {
			l.Info("unable to fetch HPA for website", "Website", req.NamespacedName)
			return r.CreateHPA(ctx, req, app, l)
		}*/

	/*

		// Let's just set the status as Unknown when no status are available
		if app.Status.Conditions == nil || len(app.Status.Conditions) == 0 {
			meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeAvailableApp, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
			if err = r.Status().Update(ctx, app); err != nil {
				log.Error(err, "Failed to update App status")
				return ctrl.Result{}, err
			}

			// Let's re-fetch the app Custom Resource after update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			// if we try to update it again in the following operations
			if err := r.Get(ctx, req.NamespacedName, app); err != nil {
				log.Error(err, "Failed to re-fetch app")
				return ctrl.Result{}, err
			}
		}

		// Let's add a finalizer. Then, we can define some operations which should
		// occurs before the custom resource to be deleted.
		// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
		if !controllerutil.ContainsFinalizer(app, appFinalizer) {
			log.Info("Adding Finalizer for App")
			if ok := controllerutil.AddFinalizer(app, appFinalizer); !ok {
				log.Error(err, "Failed to add finalizer into the custom resource")
				return ctrl.Result{Requeue: true}, nil
			}

			if err = r.Update(ctx, app); err != nil {
				log.Error(err, "Failed to update custom resource to add finalizer")
				return ctrl.Result{}, err
			}
		}

		// Check if the App instance is marked to be deleted, which is
		// indicated by the deletion timestamp being set.
		isAppMarkedToBeDeleted := app.GetDeletionTimestamp() != nil
		if isAppMarkedToBeDeleted {
			if controllerutil.ContainsFinalizer(app, appFinalizer) {
				log.Info("Performing Finalizer Operations for App before delete CR")

				// Let's add here an status "Downgrade" to define that this resource begin its process to be terminated.
				meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeDegradedApp,
					Status: metav1.ConditionUnknown, Reason: "Finalizing",
					Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", app.Name)})

				if err := r.Status().Update(ctx, app); err != nil {
					log.Error(err, "Failed to update App status")
					return ctrl.Result{}, err
				}

				// Perform all operations required before remove the finalizer and allow
				// the Kubernetes API to remove the custom resource.
				r.doFinalizerOperationsForApp(app)

				// TODO(user): If you add operations to the doFinalizerOperationsForApp method
				// then you need to ensure that all worked fine before deleting and updating the Downgrade status
				// otherwise, you should requeue here.

				// Re-fetch the app Custom Resource before update the status
				// so that we have the latest state of the resource on the cluster and we will avoid
				// raise the issue "the object has been modified, please apply
				// your changes to the latest version and try again" which would re-trigger the reconciliation
				if err := r.Get(ctx, req.NamespacedName, app); err != nil {
					log.Error(err, "Failed to re-fetch app")
					return ctrl.Result{}, err
				}

				meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeDegradedApp,
					Status: metav1.ConditionTrue, Reason: "Finalizing",
					Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", app.Name)})

				if err := r.Status().Update(ctx, app); err != nil {
					log.Error(err, "Failed to update App status")
					return ctrl.Result{}, err
				}

				log.Info("Removing Finalizer for App after successfully perform the operations")
				if ok := controllerutil.RemoveFinalizer(app, appFinalizer); !ok {
					log.Error(err, "Failed to remove finalizer for App")
					return ctrl.Result{Requeue: true}, nil
				}

				if err := r.Update(ctx, app); err != nil {
					log.Error(err, "Failed to remove finalizer for App")
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}

		// Check if the deployment already exists, if not create a new one
		found := &appsv1.Deployment{}
		err = r.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, found)
		if err != nil && apierrors.IsNotFound(err) {
			// Define a new deployment
			dep, err := r.deploymentForApp(app)
			if err != nil {
				log.Error(err, "Failed to define new Deployment resource for App")

				// The following implementation will update the status
				meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeAvailableApp,
					Status: metav1.ConditionFalse, Reason: "Reconciling",
					Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", app.Name, err)})

				if err := r.Status().Update(ctx, app); err != nil {
					log.Error(err, "Failed to update App status")
					return ctrl.Result{}, err
				}

				return ctrl.Result{}, err
			}

			log.Info("Creating a new Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			if err = r.Create(ctx, dep); err != nil {
				log.Error(err, "Failed to create new Deployment",
					"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
				return ctrl.Result{}, err
			}

			// Deployment created successfully
			// We will requeue the reconciliation so that we can ensure the state
			// and move forward for the next operations
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		} else if err != nil {
			log.Error(err, "Failed to get Deployment")
			// Let's return the error for the reconciliation be re-trigged again
			return ctrl.Result{}, err
		}

		// The CRD API is defining that the App type, have a AppSpec.Size field
		// to set the quantity of Deployment instances is the desired state on the cluster.
		// Therefore, the following code will ensure the Deployment size is the same as defined
		// via the Size spec of the Custom Resource which we are reconciling.
		size := app.Spec.Size
		if *found.Spec.Replicas != size {
			found.Spec.Replicas = &size
			if err = r.Update(ctx, found); err != nil {
				log.Error(err, "Failed to update Deployment",
					"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

				// Re-fetch the app Custom Resource before update the status
				// so that we have the latest state of the resource on the cluster and we will avoid
				// raise the issue "the object has been modified, please apply
				// your changes to the latest version and try again" which would re-trigger the reconciliation
				if err := r.Get(ctx, req.NamespacedName, app); err != nil {
					log.Error(err, "Failed to re-fetch app")
					return ctrl.Result{}, err
				}

				// The following implementation will update the status
				meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeAvailableApp,
					Status: metav1.ConditionFalse, Reason: "Resizing",
					Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", app.Name, err)})

				if err := r.Status().Update(ctx, app); err != nil {
					log.Error(err, "Failed to update App status")
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
		meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{Type: typeAvailableApp,
			Status: metav1.ConditionTrue, Reason: "Reconciling",
			Message: fmt.Sprintf("Deployment for custom resource (%s) with %d replicas created successfully", app.Name, size)})

		if err := r.Status().Update(ctx, app); err != nil {
			log.Error(err, "Failed to update App status")
			return ctrl.Result{}, err
		}
	*/
	return ctrl.Result{}, nil
}

// finalizeApp will perform the required operations before delete the CR.
func (r *AppReconciler) doFinalizerOperationsForApp(cr *appsv1.App) {
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

// deploymentForApp returns a App Deployment object
func (r *AppReconciler) deploymentForApp(
	app *appsv1.App) (*kv2.Deployment, error) {
	ls := labelsForApp(app.Name)
	// replicas := app.Spec.Size
	replicas := app.Spec.MinReplica

	// Get the Operand image
	image, err := imageForApp()
	if err != nil {
		return nil, err
	}

	dep := &kv2.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
		},
		Spec: kv2.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					// TODO(user): Uncomment the following code to configure the nodeAffinity expression
					// according to the platforms which are supported by your solution. It is considered
					// best practice to support multiple architectures. build your manager image using the
					// makefile target docker-buildx. Also, you can use docker manifest inspect <image>
					// to check what are the platforms supported.
					// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#node-affinity
					//Affinity: &corev1.Affinity{
					//	NodeAffinity: &corev1.NodeAffinity{
					//		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					//			NodeSelectorTerms: []corev1.NodeSelectorTerm{
					//				{
					//					MatchExpressions: []corev1.NodeSelectorRequirement{
					//						{
					//							Key:      "kubernetes.io/arch",
					//							Operator: "In",
					//							Values:   []string{"amd64", "arm64", "ppc64le", "s390x"},
					//						},
					//						{
					//							Key:      "kubernetes.io/os",
					//							Operator: "In",
					//							Values:   []string{"linux"},
					//						},
					//					},
					//				},
					//			},
					//		},
					//	},
					//},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
						// If you are looking for to produce solutions to be supported
						// on lower versions you must remove this option.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Image:           image,
						Name:            "app",
						ImagePullPolicy: corev1.PullIfNotPresent,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             &[]bool{true}[0],
							AllowPrivilegeEscalation: &[]bool{false}[0],
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
					}},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(app, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// labelsForApp returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForApp(name string) map[string]string {
	var imageTag string
	image, err := imageForApp()
	if err == nil {
		imageTag = strings.Split(image, ":")[1]
	}
	return map[string]string{"app.kubernetes.io/name": "App",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/version":    imageTag,
		"app.kubernetes.io/part-of":    "kube-operator-demo",
		"app.kubernetes.io/created-by": "controller-manager",
	}
}

// imageForApp gets the Operand image which is managed by this controller
// from the APP_IMAGE environment variable defined in the config/manager/manager.yaml
func imageForApp() (string, error) {
	var imageEnvVar = "APP_IMAGE"
	image, found := os.LookupEnv(imageEnvVar)
	if !found {
		return "", fmt.Errorf("unable to find %s environment variable with the image", imageEnvVar)
	}
	return image, nil
}

// UpdateStatus Update status of the website
func (r *AppReconciler) UpdateStatus(ctx context.Context, req ctrl.Request, app appsv1.App, dApp *kv2.Deployment) {
	// Here we are only maintaining the current replica count for the status, more can be done.
	app.Status.CurrentReplicas = dApp.Spec.Replicas
}

// CreateDeployment creates the deployment in the cluster.
func (r *AppReconciler) CreateDeployment(ctx context.Context, req ctrl.Request, app appsv1.App, log logr.Logger) (ctrl.Result, error) {
	appDeployment := &kv2.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    app.ObjectMeta.Labels,
			Name:      req.Name,
			Namespace: req.Namespace,
		},
		Spec: kv2.DeploymentSpec{
			Replicas: app.Spec.MinReplica,
			Selector: &metav1.LabelSelector{
				MatchLabels: app.ObjectMeta.Labels,
			},
			Template: kv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:    app.ObjectMeta.Labels,
					Name:      req.Name,
					Namespace: req.Namespace,
				},
				Spec: kv1.PodSpec{
					Containers: []kv1.Container{
						kv1.Container{
							Name:  app.Name,
							Image: app.Spec.Image,
							Ports: []kv1.ContainerPort{
								{
									ContainerPort: app.Spec.Port,
								},
							},
							ImagePullPolicy: kv1.PullIfNotPresent,
							Resources: kv1.ResourceRequirements{
								Requests: kv1.ResourceList{
									kv1.ResourceCPU: app.Spec.CPURequest,
								},
							},
						},
					},
				},
			},
		},
	}
	if err := r.Create(ctx, appDeployment); err != nil {
		log.Error(err, "unable to create app deployment for App", "app", appDeployment)
		return ctrl.Result{}, err
	}

	log.V(1).Info("created app deployment for App run", "appPod", appDeployment)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// RemoveDeployment deletes deployment from the cluster
func (r *AppReconciler) RemoveDeployment(ctx context.Context, dToRemove *kv2.Deployment, log logr.Logger) (ctrl.Result, error) {
	name := dToRemove.Name
	if err := r.Delete(ctx, dToRemove); err != nil {
		log.Error(err, "unable to delete app deployment for App", "app", dToRemove.Name)
		return ctrl.Result{}, err
	}
	log.V(1).Info("Removed app deployment for App run", "appPod", name)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// CreateService creates the desired service in the cluster
func (r *AppReconciler) CreateService(ctx context.Context, req ctrl.Request, app appsv1.App, log logr.Logger) (ctrl.Result, error) {
	appService := &kv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    app.ObjectMeta.Labels,
			Name:      req.Name,
			Namespace: req.Namespace,
		},
		Spec: kv1.ServiceSpec{
			Ports: []kv1.ServicePort{
				{
					Port: app.Spec.Port,
				},
			},
			Selector: app.ObjectMeta.Labels,
		},
	}
	if err := r.Create(ctx, appService); err != nil {
		log.Error(err, "unable to create App service for App", "app", appService)
		return ctrl.Result{}, err
	}

	log.V(1).Info("created app service for App run", "appPod", appService)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// RemoveService deletes the service from the cluster.
func (r *AppReconciler) RemoveService(ctx context.Context, serviceToRemove *kv1.Service, log logr.Logger) (ctrl.Result, error) {
	name := serviceToRemove.Name
	if err := r.Delete(ctx, serviceToRemove); err != nil {
		log.Error(err, "unable to delete app service for App", "app", serviceToRemove.Name)
		return ctrl.Result{}, err
	}
	log.V(1).Info("Removed app service for App run", "appPod", name)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

var (
	jobOwnerKey = ".metadata.controller"
	apiGVStr    = appsv1.GroupVersion.String()
)

func (r *AppReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &kv2.Deployment{}, jobOwnerKey, func(rawObj client.Object) []string {
		// grab the deployment object, extract the owner...
		deployment := rawObj.(*kv2.Deployment)
		owner := metav1.GetControllerOf(deployment)
		if owner == nil {
			return []string{}
		}
		// ...make sure it's an App type...
		if owner.APIVersion != apiGVStr || owner.Kind != "App" {
			return []string{}
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.App{}).
		Owns(&kv2.Deployment{}).
		Complete(r)
}
