/*
Copyright 2023 Chia Network Inc.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	k8schianetv1 "github.com/chia-network/chia-operator/api/v1"
	"github.com/cisco-open/operator-tools/pkg/reconciler"
)

const (
	// mainnetNodePort defines the port for mainnet nodes
	mainnetNodePort = 8444

	// testnetNodePort defines the port for testnet nodes
	testnetNodePort = 58444

	// nodeRPCPort defines the port for the full_node RPC
	nodeRPCPort = 8555
)

// ChiaNodeReconciler reconciles a ChiaNode object
type ChiaNodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=k8s.chia.net,resources=chianodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=k8s.chia.net,resources=chianodes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=k8s.chia.net,resources=chianodes/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *ChiaNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	resourceReconciler := reconciler.NewReconcilerWith(r.Client, reconciler.WithLog(log))
	log.Info(fmt.Sprintf("ChiaNodeReconciler ChiaNode=%s", req.NamespacedName.String()))

	// Get the custom resource
	var node k8schianetv1.ChiaNode
	err := r.Get(ctx, req.NamespacedName, &node)
	if err != nil && errors.IsNotFound(err) {
		// Return here, this can happen if the CR was deleted
		return ctrl.Result{}, nil
	}
	if err != nil {
		log.Error(err, fmt.Sprintf("ChiaNodeReconciler ChiaNode=%s unable to fetch ChiaNode resource", req.NamespacedName))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Reconcile ChiaNode owned objects
	srv := r.assembleBaseService(ctx, node)
	res, err := reconcileService(ctx, resourceReconciler, srv)
	if err != nil {
		if res == nil {
			res = &reconcile.Result{}
		}
		return *res, fmt.Errorf("ChiaNodeReconciler ChiaNode=%s encountered error reconciling node Service: %v", req.NamespacedName, err)
	}

	srv = r.assembleInternalService(ctx, node)
	res, err = reconcileService(ctx, resourceReconciler, srv)
	if err != nil {
		if res == nil {
			res = &reconcile.Result{}
		}
		return *res, fmt.Errorf("ChiaNodeReconciler ChiaNode=%s encountered error reconciling node Local Service: %v", req.NamespacedName, err)
	}

	srv = r.assembleHeadlessService(ctx, node)
	res, err = reconcileService(ctx, resourceReconciler, srv)
	if err != nil {
		if res == nil {
			res = &reconcile.Result{}
		}
		return *res, fmt.Errorf("ChiaNodeReconciler ChiaNode=%s encountered error reconciling node headless Service: %v", req.NamespacedName, err)
	}

	srv = r.assembleChiaExporterService(ctx, node)
	res, err = reconcileService(ctx, resourceReconciler, srv)
	if err != nil {
		if res == nil {
			res = &reconcile.Result{}
		}
		return *res, fmt.Errorf("ChiaNodeReconciler ChiaNode=%s encountered error reconciling node chia-exporter Service: %v", req.NamespacedName, err)
	}

	stateful := r.assembleStatefulset(ctx, node)
	res, err = reconcileStatefulset(ctx, resourceReconciler, stateful)
	if err != nil {
		if res == nil {
			res = &reconcile.Result{}
		}
		return *res, fmt.Errorf("ChiaNodeReconciler ChiaNode=%s encountered error reconciling node StatefulSet: %v", req.NamespacedName, err)
	}

	// Update CR status
	node.Status.Ready = true
	err = r.Status().Update(ctx, &node)
	if err != nil {
		log.Error(err, fmt.Sprintf("ChiaNodeReconciler ChiaCA=%s unable to update ChiaNode status", req.NamespacedName))
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChiaNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k8schianetv1.ChiaNode{}).
		Complete(r)
}

// assembleBaseService assembles the main Service resource for a ChiaNode CR
func (r *ChiaNodeReconciler) assembleBaseService(ctx context.Context, node k8schianetv1.ChiaNode) corev1.Service {
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-node", node.Name),
			Namespace:       node.Namespace,
			Labels:          r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
			Annotations:     node.Spec.AdditionalMetadata.Annotations,
			OwnerReferences: r.getOwnerReference(ctx, node),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceType(node.Spec.ServiceType),
			Ports: []corev1.ServicePort{
				{
					Port:       daemonPort,
					TargetPort: intstr.FromString("daemon"),
					Protocol:   "TCP",
					Name:       "daemon",
				},
				{
					Port:       r.getFullNodePort(ctx, node),
					TargetPort: intstr.FromString("peers"),
					Protocol:   "TCP",
					Name:       "peers",
				},
				{
					Port:       nodeRPCPort,
					TargetPort: intstr.FromString("rpc"),
					Protocol:   "TCP",
					Name:       "rpc",
				},
			},
			Selector: r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
		},
	}
}

// assembleInternalService assembles the internal Service resource for a ChiaNode CR
func (r *ChiaNodeReconciler) assembleInternalService(ctx context.Context, node k8schianetv1.ChiaNode) corev1.Service {
	local := corev1.ServiceInternalTrafficPolicyLocal
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-node-internal", node.Name),
			Namespace:       node.Namespace,
			Labels:          r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
			Annotations:     node.Spec.AdditionalMetadata.Annotations,
			OwnerReferences: r.getOwnerReference(ctx, node),
		},
		Spec: corev1.ServiceSpec{
			Type:                  corev1.ServiceType("ClusterIP"),
			InternalTrafficPolicy: &local,
			Ports: []corev1.ServicePort{
				{
					Port:       daemonPort,
					TargetPort: intstr.FromString("daemon"),
					Protocol:   "TCP",
					Name:       "daemon",
				},
				{
					Port:       r.getFullNodePort(ctx, node),
					TargetPort: intstr.FromString("peers"),
					Protocol:   "TCP",
					Name:       "peers",
				},
				{
					Port:       nodeRPCPort,
					TargetPort: intstr.FromString("rpc"),
					Protocol:   "TCP",
					Name:       "rpc",
				},
			},
			Selector: r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
		},
	}
}

// assembleHeadlessService assembles the headless Service resource for a ChiaNode CR
func (r *ChiaNodeReconciler) assembleHeadlessService(ctx context.Context, node k8schianetv1.ChiaNode) corev1.Service {
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-node-headless", node.Name),
			Namespace:       node.Namespace,
			Labels:          r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
			Annotations:     node.Spec.AdditionalMetadata.Annotations,
			OwnerReferences: r.getOwnerReference(ctx, node),
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceType("ClusterIP"),
			ClusterIP: "None",
			Ports: []corev1.ServicePort{
				{
					Port:       daemonPort,
					TargetPort: intstr.FromString("daemon"),
					Protocol:   "TCP",
					Name:       "daemon",
				},
				{
					Port:       r.getFullNodePort(ctx, node),
					TargetPort: intstr.FromString("peers"),
					Protocol:   "TCP",
					Name:       "peers",
				},
				{
					Port:       nodeRPCPort,
					TargetPort: intstr.FromString("rpc"),
					Protocol:   "TCP",
					Name:       "rpc",
				},
			},
			Selector: r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
		},
	}
}

// assembleChiaExporterService assembles the chia-exporter Service resource for a ChiaNode CR
func (r *ChiaNodeReconciler) assembleChiaExporterService(ctx context.Context, node k8schianetv1.ChiaNode) corev1.Service {
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-node-metrics", node.Name),
			Namespace:       node.Namespace,
			Labels:          r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels, node.Spec.ChiaExporterConfig.ServiceLabels),
			Annotations:     node.Spec.AdditionalMetadata.Annotations,
			OwnerReferences: r.getOwnerReference(ctx, node),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceType("ClusterIP"),
			Ports: []corev1.ServicePort{
				{
					Port:       chiaExporterPort,
					TargetPort: intstr.FromString("metrics"),
					Protocol:   "TCP",
					Name:       "metrics",
				},
			},
			Selector: r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
		},
	}
}

// assembleStatefulset assembles the node StatefulSet resource for a ChiaNode CR
func (r *ChiaNodeReconciler) assembleStatefulset(ctx context.Context, node k8schianetv1.ChiaNode) appsv1.StatefulSet {
	var chiaSecContext *corev1.SecurityContext
	if node.Spec.ChiaConfig.SecurityContext != nil {
		chiaSecContext = node.Spec.ChiaConfig.SecurityContext
	}

	var chiaLivenessProbe *corev1.Probe
	if node.Spec.ChiaConfig.LivenessProbe != nil {
		chiaLivenessProbe = node.Spec.ChiaConfig.LivenessProbe
	}

	var chiaReadinessProbe *corev1.Probe
	if node.Spec.ChiaConfig.ReadinessProbe != nil {
		chiaReadinessProbe = node.Spec.ChiaConfig.ReadinessProbe
	}

	var chiaStartupProbe *corev1.Probe
	if node.Spec.ChiaConfig.StartupProbe != nil {
		chiaStartupProbe = node.Spec.ChiaConfig.StartupProbe
	}

	var chiaResources corev1.ResourceRequirements
	if node.Spec.ChiaConfig.Resources != nil {
		chiaResources = *node.Spec.ChiaConfig.Resources
	}

	var imagePullPolicy corev1.PullPolicy
	if node.Spec.ImagePullPolicy != nil {
		imagePullPolicy = *node.Spec.ImagePullPolicy
	}

	var chiaExporterImage = node.Spec.ChiaExporterConfig.Image
	if chiaExporterImage == "" {
		chiaExporterImage = defaultChiaExporterImage
	}

	vols, volClaimTemplates := r.getChiaVolumesAndTemplates(ctx, node)

	var stateful appsv1.StatefulSet = appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-node", node.Name),
			Namespace:       node.Namespace,
			Labels:          r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
			Annotations:     node.Spec.AdditionalMetadata.Annotations,
			OwnerReferences: r.getOwnerReference(ctx, node),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: node.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: r.getCommonLabels(ctx, node),
			},
			ServiceName: fmt.Sprintf("%s-headless", node.Name),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      r.getCommonLabels(ctx, node, node.Spec.AdditionalMetadata.Labels),
					Annotations: node.Spec.AdditionalMetadata.Annotations,
				},
				Spec: corev1.PodSpec{
					// TODO add: imagePullSecret, serviceAccountName config
					Containers: []corev1.Container{
						{
							Name:            "chia",
							SecurityContext: chiaSecContext,
							Image:           node.Spec.ChiaConfig.Image,
							ImagePullPolicy: imagePullPolicy,
							Env:             r.getChiaNodeEnv(ctx, node),
							Ports: []corev1.ContainerPort{
								{
									Name:          "daemon",
									ContainerPort: daemonPort,
									Protocol:      "TCP",
								},
								{
									Name:          "peers",
									ContainerPort: r.getFullNodePort(ctx, node),
									Protocol:      "TCP",
								},
								{
									Name:          "rpc",
									ContainerPort: nodeRPCPort,
									Protocol:      "TCP",
								},
							},
							LivenessProbe:  chiaLivenessProbe,
							ReadinessProbe: chiaReadinessProbe,
							StartupProbe:   chiaStartupProbe,
							Resources:      chiaResources,
							VolumeMounts:   r.getChiaVolumeMounts(ctx, node),
						},
					},
					NodeSelector: node.Spec.NodeSelector,
					Volumes:      vols,
				},
			},
			VolumeClaimTemplates: volClaimTemplates,
		},
	}

	exporterContainer := getChiaExporterContainer(ctx, chiaExporterImage, chiaSecContext, imagePullPolicy, chiaResources)
	stateful.Spec.Template.Spec.Containers = append(stateful.Spec.Template.Spec.Containers, exporterContainer)

	if node.Spec.PodSecurityContext != nil {
		stateful.Spec.Template.Spec.SecurityContext = node.Spec.PodSecurityContext
	}

	// TODO add pod affinity, tolerations

	return stateful
}

// getChiaVolumes retrieves the requisite volumes from the Chia config struct
func (r *ChiaNodeReconciler) getChiaVolumesAndTemplates(ctx context.Context, node k8schianetv1.ChiaNode) ([]corev1.Volume, []corev1.PersistentVolumeClaim) {
	var v []corev1.Volume
	var vcts []corev1.PersistentVolumeClaim

	// secret ca volume
	v = append(v, corev1.Volume{
		Name: "secret-ca",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: node.Spec.ChiaConfig.CASecretName,
			},
		},
	})

	// CHIA_ROOT volume -- PVC is respected first if both it and hostpath are specified, falls back to hostPath if specified
	// If both are empty, fall back to emptyDir so chia-exporter can mount CHIA_ROOT
	var chiaRootAdded bool = false
	if node.Spec.Storage != nil && node.Spec.Storage.ChiaRoot != nil {
		if node.Spec.Storage.ChiaRoot.PersistentVolumeClaim != nil {
			vcts = append(vcts, corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: "chiaroot",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{"ReadWriteOnce"},
					StorageClassName: &node.Spec.Storage.ChiaRoot.PersistentVolumeClaim.StorageClass,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse(node.Spec.Storage.ChiaRoot.PersistentVolumeClaim.ResourceRequest),
						},
					},
				},
			})
			chiaRootAdded = true
		} else if node.Spec.Storage.ChiaRoot.HostPathVolume != nil {
			v = append(v, corev1.Volume{
				Name: "chiaroot",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: node.Spec.Storage.ChiaRoot.HostPathVolume.Path,
					},
				},
			})
			chiaRootAdded = true
		}
	}
	if !chiaRootAdded {
		v = append(v, corev1.Volume{
			Name: "chiaroot",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	return v, vcts
}

// getChiaVolumeMounts retrieves the requisite volume mounts from the Chia config struct
func (r *ChiaNodeReconciler) getChiaVolumeMounts(ctx context.Context, node k8schianetv1.ChiaNode) []corev1.VolumeMount {
	var v []corev1.VolumeMount

	// secret ca volume
	v = append(v, corev1.VolumeMount{
		Name:      "secret-ca",
		MountPath: "/chia-ca",
	})

	// CHIA_ROOT volume
	v = append(v, corev1.VolumeMount{
		Name:      "chiaroot",
		MountPath: "/chia-data",
	})

	return v
}

// getChiaNodeEnv retrieves the environment variables from the Chia config struct
func (r *ChiaNodeReconciler) getChiaNodeEnv(ctx context.Context, node k8schianetv1.ChiaNode) []corev1.EnvVar {
	var env []corev1.EnvVar

	// service env var
	env = append(env, corev1.EnvVar{
		Name:  "service",
		Value: "node",
	})

	// CHIA_ROOT env var
	env = append(env, corev1.EnvVar{
		Name:  "CHIA_ROOT",
		Value: "/chia-data",
	})

	// keys env var -- no keys required for a node
	env = append(env, corev1.EnvVar{
		Name:  "keys",
		Value: "none",
	})

	// ca env var
	env = append(env, corev1.EnvVar{
		Name:  "ca",
		Value: "/chia-ca",
	})

	// testnet env var
	if node.Spec.ChiaConfig.Testnet != nil && *node.Spec.ChiaConfig.Testnet {
		env = append(env, corev1.EnvVar{
			Name:  "testnet",
			Value: "true",
		})
	}

	// TZ env var
	if node.Spec.ChiaConfig.Timezone != nil {
		env = append(env, corev1.EnvVar{
			Name:  "TZ",
			Value: *node.Spec.ChiaConfig.Timezone,
		})
	}

	// log_level env var
	if node.Spec.ChiaConfig.LogLevel != nil {
		env = append(env, corev1.EnvVar{
			Name:  "log_level",
			Value: *node.Spec.ChiaConfig.LogLevel,
		})
	}

	return env
}

// getCommonLabels gives some common labels for ChiaNode related objects
func (r *ChiaNodeReconciler) getCommonLabels(ctx context.Context, node k8schianetv1.ChiaNode, additionalLabels ...map[string]string) map[string]string {
	var labels = make(map[string]string)
	for _, addition := range additionalLabels {
		for k, v := range addition {
			labels[k] = v
		}
	}
	labels["app.kubernetes.io/instance"] = node.Name
	labels["chianode-owner"] = node.Name
	labels = getCommonLabels(ctx, labels)
	return labels
}

// getOwnerReference gives the common owner reference spec for ChiaNode related objects
func (r *ChiaNodeReconciler) getOwnerReference(ctx context.Context, node k8schianetv1.ChiaNode) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		{
			APIVersion: node.APIVersion,
			Kind:       node.Kind,
			Name:       node.Name,
			UID:        node.UID,
			Controller: &controllerOwner,
		},
	}
}

// getFullNodePort determines the correct full node port to use
func (r *ChiaNodeReconciler) getFullNodePort(ctx context.Context, node k8schianetv1.ChiaNode) int32 {
	if node.Spec.ChiaConfig.Testnet != nil {
		if *node.Spec.ChiaConfig.Testnet {
			return testnetNodePort
		} else {
			return mainnetNodePort
		}
	}
	return mainnetNodePort
}
