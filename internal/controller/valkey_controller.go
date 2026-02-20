// Package controller implements the Kubernetes reconciliation logic
// for Valkey custom resources.
package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// InstanceChecker verifies connectivity and health of Valkey instances.
// Implementations must provide PingPod for basic connectivity checks and
// CheckCluster for full HA cluster health verification.
type InstanceChecker interface {
	PingPod(ctx context.Context, v *vkov1.Valkey, podName string) error
	CheckCluster(ctx context.Context, v *vkov1.Valkey) *health.ClusterState
}

// ValkeyReconciler reconciles a Valkey object.
type ValkeyReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	InstanceChecker InstanceChecker
}

// getInstanceChecker returns the configured InstanceChecker or creates a default one.
func (r *ValkeyReconciler) getInstanceChecker() InstanceChecker {
	if r.InstanceChecker != nil {
		return r.InstanceChecker
	}
	return health.NewChecker(r.Client)
}

// buildTLSConfig reads the TLS CA certificate from the specified Secret
// and returns a tls.Config suitable for connecting to TLS-enabled Valkey/Sentinel pods.
// Returns nil if TLS is not enabled.
func (r *ValkeyReconciler) buildTLSConfig(ctx context.Context, v *vkov1.Valkey, secretName string) (*tls.Config, error) {
	if !v.IsTLSEnabled() {
		return nil, nil
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: v.Namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("reading TLS secret %s: %w", secretName, err)
	}

	caCert, ok := secret.Data["ca.crt"]
	if !ok {
		return nil, fmt.Errorf("TLS secret %s missing ca.crt", secretName)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate from secret %s", secretName)
	}

	return &tls.Config{
		RootCAs:    certPool,
		MinVersion: tls.VersionTLS12,
	}, nil
}

// newValkeyClient creates a Valkey RESP client, using TLS if tlsConfig is non-nil.
func (r *ValkeyReconciler) newValkeyClient(addr string, tlsConfig *tls.Config) *valkeyclient.Client {
	if tlsConfig != nil {
		return valkeyclient.NewTLS(addr, tlsConfig)
	}
	return valkeyclient.New(addr)
}

// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vko.gtrfc.com,resources=valkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles a reconciliation request for a Valkey resource.
func (r *ValkeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Valkey instance.
	valkey := &vkov1.Valkey{}
	if err := r.Get(ctx, req.NamespacedName, valkey); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Valkey resource not found, probably deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial provisioning status if phase is empty.
	if valkey.Status.Phase == "" {
		if err := r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseProvisioning, "Setting up Valkey resources"); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile all managed resources.
	if err := r.reconcileResources(ctx, valkey); err != nil {
		return ctrl.Result{}, err
	}

	// Check for rolling update (image change on running pods).
	rollingResult := r.checkAndHandleRollingUpdate(ctx, valkey)
	if rollingResult.Error != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Rolling update error: %v", rollingResult.Error))
		return ctrl.Result{}, rollingResult.Error
	}
	if rollingResult.NeedsRequeue {
		return ctrl.Result{RequeueAfter: rollingResult.RequeueAfter}, nil
	}

	// Update status based on StatefulSet readiness.
	if err := r.updateStatus(ctx, valkey); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue when the instance is not yet healthy so transient connectivity
	// failures (e.g. pod just restarted) are retried automatically.
	// Status-only updates do not trigger the GenerationChangedPredicate,
	// so without this explicit requeue the resource would stay in Error/Syncing.
	if valkey.Status.Phase == vkov1.ValkeyPhaseError || valkey.Status.Phase == vkov1.ValkeyphaseSyncing {
		logger.Info("Instance not healthy, requeuing", "phase", valkey.Status.Phase)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileResources reconciles all Kubernetes resources managed by the operator.
func (r *ValkeyReconciler) reconcileResources(ctx context.Context, valkey *vkov1.Valkey) error {
	// Reconcile ConfigMap.
	if err := r.reconcileConfigMap(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile ConfigMap: %v", err))
		return err
	}

	// Reconcile replica ConfigMap in HA mode.
	if valkey.IsSentinelEnabled() {
		if err := r.reconcileReplicaConfigMap(ctx, valkey); err != nil {
			_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile replica ConfigMap: %v", err))
			return err
		}
	}

	// Reconcile TLS Certificates (cert-manager) if enabled.
	if valkey.IsCertManagerEnabled() {
		if err := r.reconcileTLSCertificates(ctx, valkey); err != nil {
			_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile TLS Certificates: %v", err))
			return err
		}
	}

	// Reconcile headless Service.
	if err := r.reconcileHeadlessService(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile headless Service: %v", err))
		return err
	}

	// Reconcile client Service.
	if err := r.reconcileClientService(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile client Service: %v", err))
		return err
	}

	// Reconcile StatefulSet.
	if err := r.reconcileStatefulSet(ctx, valkey); err != nil {
		_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile StatefulSet: %v", err))
		return err
	}

	// Reconcile Sentinel resources if enabled.
	if valkey.IsSentinelEnabled() {
		if err := r.reconcileSentinelResources(ctx, valkey); err != nil {
			_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile Sentinel resources: %v", err))
			return err
		}
	}

	// Reconcile NetworkPolicies if enabled.
	if valkey.IsNetworkPolicyEnabled() {
		if err := r.reconcileNetworkPolicies(ctx, valkey); err != nil {
			_ = r.updatePhase(ctx, valkey, vkov1.ValkeyPhaseError, fmt.Sprintf("Failed to reconcile NetworkPolicies: %v", err))
			return err
		}
	}

	return nil
}

// reconcileConfigMap ensures the Valkey ConfigMap matches the desired state.
func (r *ValkeyReconciler) reconcileConfigMap(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildConfigMap(v)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ConfigMap: %w", err)
	}

	current := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating ConfigMap", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update if config content has changed.
	if !equality.Semantic.DeepEqual(current.Data, desired.Data) {
		logger.Info("Updating ConfigMap", "name", desired.Name)
		current.Data = desired.Data
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileReplicaConfigMap ensures the replica ConfigMap exists in HA mode.
func (r *ValkeyReconciler) reconcileReplicaConfigMap(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildReplicaConfigMap(v)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on replica ConfigMap: %w", err)
	}

	current := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating replica ConfigMap", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepEqual(current.Data, desired.Data) {
		logger.Info("Updating replica ConfigMap", "name", desired.Name)
		current.Data = desired.Data
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileHeadlessService ensures the headless Service exists and matches the desired state.
func (r *ValkeyReconciler) reconcileHeadlessService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildHeadlessService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileClientService ensures the client-facing Service exists and matches the desired state.
func (r *ValkeyReconciler) reconcileClientService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildClientService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileService is a generic service reconciler.
func (r *ValkeyReconciler) reconcileService(ctx context.Context, v *vkov1.Valkey, desired *corev1.Service) error {
	logger := log.FromContext(ctx)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Service %s: %w", desired.Name, err)
	}

	current := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Service", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update ports, selector, labels if they changed.
	if !equality.Semantic.DeepEqual(current.Spec.Ports, desired.Spec.Ports) ||
		!equality.Semantic.DeepEqual(current.Spec.Selector, desired.Spec.Selector) {
		logger.Info("Updating Service", "name", desired.Name)
		current.Spec.Ports = desired.Spec.Ports
		current.Spec.Selector = desired.Spec.Selector
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileStatefulSet ensures the StatefulSet exists and matches the desired state.
func (r *ValkeyReconciler) reconcileStatefulSet(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildStatefulSet(v)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on StatefulSet: %w", err)
	}

	current := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating StatefulSet", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Detect drift and update.
	if builder.StatefulSetHasChanged(desired, current) {
		logger.Info("Updating StatefulSet", "name", desired.Name)
		current.Spec.Replicas = desired.Spec.Replicas
		current.Spec.Template = desired.Spec.Template
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileSentinelResources reconciles all Sentinel-related resources.
func (r *ValkeyReconciler) reconcileSentinelResources(ctx context.Context, v *vkov1.Valkey) error {
	// Sentinel ConfigMap.
	if err := r.reconcileSentinelConfigMap(ctx, v); err != nil {
		return fmt.Errorf("sentinel configmap: %w", err)
	}

	// Sentinel headless Service.
	if err := r.reconcileSentinelHeadlessService(ctx, v); err != nil {
		return fmt.Errorf("sentinel headless service: %w", err)
	}

	// Sentinel StatefulSet.
	if err := r.reconcileSentinelStatefulSet(ctx, v); err != nil {
		return fmt.Errorf("sentinel statefulset: %w", err)
	}

	return nil
}

// reconcileSentinelConfigMap ensures the Sentinel ConfigMap matches the desired state.
func (r *ValkeyReconciler) reconcileSentinelConfigMap(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildSentinelConfigMap(v)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Sentinel ConfigMap: %w", err)
	}

	current := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Sentinel ConfigMap", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepEqual(current.Data, desired.Data) {
		logger.Info("Updating Sentinel ConfigMap", "name", desired.Name)
		current.Data = desired.Data
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileSentinelHeadlessService ensures the Sentinel headless Service exists.
func (r *ValkeyReconciler) reconcileSentinelHeadlessService(ctx context.Context, v *vkov1.Valkey) error {
	desired := builder.BuildSentinelHeadlessService(v)
	return r.reconcileService(ctx, v, desired)
}

// reconcileSentinelStatefulSet ensures the Sentinel StatefulSet exists and matches desired state.
func (r *ValkeyReconciler) reconcileSentinelStatefulSet(ctx context.Context, v *vkov1.Valkey) error {
	logger := log.FromContext(ctx)
	desired := builder.BuildSentinelStatefulSet(v)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Sentinel StatefulSet: %w", err)
	}

	current := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Sentinel StatefulSet", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if builder.SentinelStatefulSetHasChanged(desired, current) {
		logger.Info("Updating Sentinel StatefulSet", "name", desired.Name)
		current.Spec.Replicas = desired.Spec.Replicas
		current.Spec.Template = desired.Spec.Template
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// reconcileTLSCertificates reconciles cert-manager Certificate resources for TLS.
func (r *ValkeyReconciler) reconcileTLSCertificates(ctx context.Context, v *vkov1.Valkey) error {
	// Reconcile Valkey Certificate.
	desired := builder.BuildValkeyCertificate(v)
	if err := r.reconcileCertificate(ctx, v, desired); err != nil {
		return fmt.Errorf("valkey certificate: %w", err)
	}

	// Reconcile Sentinel Certificate if Sentinel is enabled.
	if v.IsSentinelEnabled() {
		desiredSentinel := builder.BuildSentinelCertificate(v)
		if err := r.reconcileCertificate(ctx, v, desiredSentinel); err != nil {
			return fmt.Errorf("sentinel certificate: %w", err)
		}
	}

	return nil
}

// reconcileCertificate ensures a cert-manager Certificate resource matches the desired state.
func (r *ValkeyReconciler) reconcileCertificate(ctx context.Context, v *vkov1.Valkey, desired *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)

	// Set owner reference manually for unstructured objects.
	ownerRef := builder.CertificateOwnerRef(v)
	blockOwnerDeletion := true
	isController := true
	ownerRef.BlockOwnerDeletion = &blockOwnerDeletion
	ownerRef.Controller = &isController
	desired.SetOwnerReferences([]metav1.OwnerReference{ownerRef})

	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})

	name := desired.GetName()
	namespace := desired.GetNamespace()

	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Certificate", "name", name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Compare spec content to determine if update is needed.
	// Remove fields that cert-manager's webhook adds/manages to avoid
	// infinite update loops (e.g., privateKey.rotationPolicy added in v1.18.0).
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	currentSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
	cleanseCertificateSpec(desiredSpec)
	cleanseCertificateSpec(currentSpec)

	if !equality.Semantic.DeepEqual(desiredSpec, currentSpec) {
		logger.Info("Updating Certificate", "name", name)
		current.Object["spec"] = desired.Object["spec"]
		current.SetLabels(desired.GetLabels())
		current.SetOwnerReferences(desired.GetOwnerReferences())
		return r.Update(ctx, current)
	}

	return nil
}

// cleanseCertificateSpec removes fields from a cert-manager Certificate spec
// that are added or managed by the cert-manager admission webhook.
// This prevents the operator from fighting with the webhook over defaulted fields
// (e.g., privateKey.rotationPolicy added in cert-manager v1.18.0).
func cleanseCertificateSpec(spec map[string]interface{}) {
	if spec == nil {
		return
	}
	// cert-manager's webhook adds spec.privateKey with defaults.
	// Remove the entire privateKey field if we didn't explicitly set it.
	delete(spec, "privateKey")
}

// reconcileNetworkPolicies reconciles all NetworkPolicy resources.
func (r *ValkeyReconciler) reconcileNetworkPolicies(ctx context.Context, v *vkov1.Valkey) error {
	// Valkey NetworkPolicy.
	desiredValkey := builder.BuildValkeyNetworkPolicy(v)
	if err := r.reconcileNetworkPolicy(ctx, v, desiredValkey); err != nil {
		return fmt.Errorf("valkey networkpolicy: %w", err)
	}

	// Sentinel NetworkPolicy (only if Sentinel is enabled).
	if v.IsSentinelEnabled() {
		desiredSentinel := builder.BuildSentinelNetworkPolicy(v)
		if err := r.reconcileNetworkPolicy(ctx, v, desiredSentinel); err != nil {
			return fmt.Errorf("sentinel networkpolicy: %w", err)
		}
	}

	return nil
}

// reconcileNetworkPolicy ensures a single NetworkPolicy matches the desired state.
func (r *ValkeyReconciler) reconcileNetworkPolicy(ctx context.Context, v *vkov1.Valkey, desired *networkingv1.NetworkPolicy) error {
	logger := log.FromContext(ctx)

	if err := controllerutil.SetControllerReference(v, desired, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on NetworkPolicy %s: %w", desired.Name, err)
	}

	current := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating NetworkPolicy", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if builder.NetworkPolicyHasChanged(desired, current) {
		logger.Info("Updating NetworkPolicy", "name", desired.Name)
		current.Spec = desired.Spec
		current.Labels = desired.Labels
		return r.Update(ctx, current)
	}

	return nil
}

// updateStatus reads the current StatefulSet and updates the Valkey status accordingly.
func (r *ValkeyReconciler) updateStatus(ctx context.Context, v *vkov1.Valkey) error {
	sts := &appsv1.StatefulSet{}
	stsName := types.NamespacedName{
		Name:      builder.ClientServiceName(v),
		Namespace: v.Namespace,
	}

	if err := r.Get(ctx, stsName, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return r.updatePhase(ctx, v, vkov1.ValkeyPhaseProvisioning, "Waiting for StatefulSet creation")
		}
		return err
	}

	// Refresh the Valkey object to avoid conflicts.
	if err := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v); err != nil {
		return err
	}

	readyReplicas := sts.Status.ReadyReplicas
	v.Status.ReadyReplicas = readyReplicas

	// In HA mode, also check Sentinel readiness.
	if v.IsSentinelEnabled() {
		return r.updateHAStatus(ctx, v, readyReplicas)
	}

	// Standalone mode status logic.
	return r.updateStandaloneStatus(ctx, v, readyReplicas)
}

// updateStandaloneStatus updates the status for standalone mode.
func (r *ValkeyReconciler) updateStandaloneStatus(ctx context.Context, v *vkov1.Valkey, readyReplicas int32) error {
	// Capture previous status to detect changes.
	prevStatus := v.Status.DeepCopy()

	switch {
	case readyReplicas == v.Spec.Replicas:
		// Verify actual connectivity to Valkey instances before reporting OK.
		if err := r.verifyValkeyConnectivity(ctx, v); err != nil {
			v.Status.Phase = vkov1.ValkeyPhaseError
			v.Status.Message = fmt.Sprintf("Instance unreachable: %v", err)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: v.Generation,
				Reason:             "ConnectivityCheckFailed",
				Message:            fmt.Sprintf("Operator cannot reach Valkey instance: %v", err),
			})
		} else {
			v.Status.Phase = vkov1.ValkeyPhaseOK
			v.Status.Message = "All replicas are ready"

			// In standalone mode, the single pod is the master.
			if v.Spec.Replicas == 1 {
				v.Status.MasterPod = fmt.Sprintf("%s-0", v.Name)
			}

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: v.Generation,
				Reason:             "AllReplicasReady",
				Message:            "All Valkey replicas are ready",
			})
		}
	case readyReplicas > 0:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = fmt.Sprintf("Waiting for replicas: %d/%d ready", readyReplicas, v.Spec.Replicas)

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "ReplicasNotReady",
			Message:            fmt.Sprintf("%d/%d replicas ready", readyReplicas, v.Spec.Replicas),
		})
	default:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = "Waiting for replicas to become ready"

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "NoReplicasReady",
			Message:            "No replicas are ready yet",
		})
	}

	// Only update if status actually changed to prevent infinite reconcile loops.
	if statusUnchanged(prevStatus, &v.Status) {
		return nil
	}

	return r.Status().Update(ctx, v)
}

// updateHAStatus updates the status for HA (Sentinel) mode.
func (r *ValkeyReconciler) updateHAStatus(ctx context.Context, v *vkov1.Valkey, readyReplicas int32) error {
	sentinelReady := int32(0)

	// Check Sentinel StatefulSet readiness.
	sentinelSts := &appsv1.StatefulSet{}
	sentinelName := types.NamespacedName{
		Name:      fmt.Sprintf("%s-sentinel", v.Name),
		Namespace: v.Namespace,
	}
	if err := r.Get(ctx, sentinelName, sentinelSts); err == nil {
		sentinelReady = sentinelSts.Status.ReadyReplicas
	}

	expectedSentinels := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		expectedSentinels = v.Spec.Sentinel.Replicas
	}

	allValkeyReady := readyReplicas == v.Spec.Replicas
	allSentinelReady := sentinelReady == expectedSentinels

	// Capture previous status to detect changes.
	prevStatus := v.Status.DeepCopy()

	switch {
	case allValkeyReady && allSentinelReady:
		// Verify actual cluster health before reporting OK.
		checker := r.getInstanceChecker()
		clusterState := checker.CheckCluster(ctx, v)

		if clusterState.Error != nil {
			v.Status.Phase = vkov1.ValkeyPhaseError
			v.Status.Message = fmt.Sprintf("Cluster health check failed: %v", clusterState.Error)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: v.Generation,
				Reason:             "ClusterHealthCheckFailed",
				Message:            fmt.Sprintf("Operator cannot verify cluster health: %v", clusterState.Error),
			})
		} else if !clusterState.AllSynced {
			v.Status.Phase = vkov1.ValkeyphaseSyncing
			v.Status.MasterPod = clusterState.MasterPod
			v.Status.Message = fmt.Sprintf("Replication syncing: %d/%d replicas ready",
				clusterState.ReadyReplicas, clusterState.TotalReplicas)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				ObservedGeneration: v.Generation,
				Reason:             "ReplicationSyncing",
				Message:            fmt.Sprintf("Replication in progress: %d/%d replicas synced", clusterState.ReadyReplicas, clusterState.TotalReplicas),
			})
		} else {
			v.Status.Phase = vkov1.ValkeyPhaseOK
			v.Status.MasterPod = clusterState.MasterPod
			v.Status.Message = fmt.Sprintf("HA cluster ready: %d/%d valkey, %d/%d sentinel",
				readyReplicas, v.Spec.Replicas, sentinelReady, expectedSentinels)

			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: v.Generation,
				Reason:             "HAClusterReady",
				Message:            "All Valkey and Sentinel instances are ready",
			})
		}
	case readyReplicas > 0 || sentinelReady > 0:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = fmt.Sprintf("HA cluster provisioning: %d/%d valkey, %d/%d sentinel",
			readyReplicas, v.Spec.Replicas, sentinelReady, expectedSentinels)

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "HAClusterProvisioning",
			Message: fmt.Sprintf("Valkey: %d/%d, Sentinel: %d/%d ready",
				readyReplicas, v.Spec.Replicas, sentinelReady, expectedSentinels),
		})
	default:
		v.Status.Phase = vkov1.ValkeyPhaseProvisioning
		v.Status.Message = "Waiting for HA cluster pods to become ready"

		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: v.Generation,
			Reason:             "HAClusterNotReady",
			Message:            "No HA cluster pods are ready yet",
		})
	}

	// Only update if status actually changed to prevent infinite reconcile loops.
	if statusUnchanged(prevStatus, &v.Status) {
		return nil
	}

	return r.Status().Update(ctx, v)
}

// statusUnchanged compares the key fields of two ValkeyStatus values.
// It returns true if phase, message, readyReplicas, masterPod, and conditions
// are all equal, meaning no status update is necessary.
func statusUnchanged(prev, curr *vkov1.ValkeyStatus) bool {
	if prev.Phase != curr.Phase {
		return false
	}
	if prev.Message != curr.Message {
		return false
	}
	if prev.ReadyReplicas != curr.ReadyReplicas {
		return false
	}
	if prev.MasterPod != curr.MasterPod {
		return false
	}
	if !reflect.DeepEqual(prev.Conditions, curr.Conditions) {
		return false
	}
	return true
}

// updatePhase is a convenience function to update only the phase and message.
func (r *ValkeyReconciler) updatePhase(ctx context.Context, v *vkov1.Valkey, phase vkov1.ValkeyPhase, message string) error {
	// Refresh the object first to avoid update conflicts.
	if err := r.Get(ctx, types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, v); err != nil {
		return err
	}

	// Skip update if nothing changed.
	if v.Status.Phase == phase && v.Status.Message == message {
		return nil
	}

	v.Status.Phase = phase
	v.Status.Message = message
	return r.Status().Update(ctx, v)
}

// verifyValkeyConnectivity pings all Valkey pods to verify operator connectivity.
// Returns nil if all pods respond, or the first error encountered.
func (r *ValkeyReconciler) verifyValkeyConnectivity(ctx context.Context, v *vkov1.Valkey) error {
	checker := r.getInstanceChecker()
	for i := int32(0); i < v.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", v.Name, i)
		if err := checker.PingPod(ctx, v, podName); err != nil {
			return fmt.Errorf("%s: %w", podName, err)
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValkeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vkov1.Valkey{}, ctrlbuilder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findValkeyForSecret),
		).
		Complete(r)
}

// findValkeyForSecret maps a Secret change to the Valkey resources that reference it.
// This ensures reconciliation triggers when a referenced auth Secret changes.
func (r *ValkeyReconciler) findValkeyForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)

	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	// List all Valkey resources in the same namespace.
	valkeyList := &vkov1.ValkeyList{}
	if err := r.List(ctx, valkeyList, client.InNamespace(secret.Namespace)); err != nil {
		logger.Error(err, "failed to list Valkey resources for Secret watch")
		return nil
	}

	var requests []reconcile.Request
	for i := range valkeyList.Items {
		v := &valkeyList.Items[i]
		if v.IsAuthEnabled() && v.Spec.Auth.SecretName == secret.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      v.Name,
					Namespace: v.Namespace,
				},
			})
		}
	}

	if len(requests) > 0 {
		logger.Info("Secret changed, triggering reconcile for Valkey resources",
			"secret", secret.Name, "count", len(requests))
	}

	return requests
}
