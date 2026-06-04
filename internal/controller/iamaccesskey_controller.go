/*
Copyright 2026.

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
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	awsaccesskeyoperatorv1alpha1 "github.com/LightJack05/AWS-AccessKey-Operator/api/v1alpha1"
)

// IAMAccessKeyReconciler reconciles a IAMAccessKey object
type IAMAccessKeyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=aws-accesskey-operator.lightjack.de,resources=iamaccesskeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aws-accesskey-operator.lightjack.de,resources=iamaccesskeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aws-accesskey-operator.lightjack.de,resources=iamaccesskeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=aws-accesskey-operator.lightjack.de,resources=iamproviderconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=aws-accesskey-operator.lightjack.de,resources=iamprovidergrants,verbs=get;list;watch
// Allow the operator to read/write secrets required
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the IAMAccessKey object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *IAMAccessKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("reconciling object:", "namespace", req.Namespace, "name", req.Name)

	// Fetch the IAMAccessKey instance
	accessKey := &awsaccesskeyoperatorv1alpha1.IAMAccessKey{}
	err := r.Client.Get(ctx, req.NamespacedName, accessKey)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("IAMAccessKey resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get IAMAccessKey resource")
		return ctrl.Result{}, err
	}

	// Verify an IAMProviderGrant in this namespace permits the requested provider and username
	permitted, err := r.isPermittedByGrant(ctx, accessKey)
	if err != nil {
		_ = r.handleGeneralReconcileError(ctx, accessKey, err)
		return ctrl.Result{}, err
	}
	if !permitted {
		// Access to this user is not permitted for this user
		accessKeySecret := &corev1.Secret{}
		err := r.Client.Get(ctx, client.ObjectKey{
			Namespace: accessKey.Namespace,
			Name:      accessKey.Spec.SecretName,
		}, accessKeySecret)
		if err == nil {
			if !isOwnedByAccessKey(accessKeySecret, accessKey) {
				if err := r.handleSecretConflict(ctx, accessKey); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			err = r.deleteSecret(ctx, accessKeySecret)
			if err != nil {
				// Couldn't delete the secret, put the object into error state and requeue
				_ = r.handleGeneralReconcileError(ctx, accessKey, err)
				return ctrl.Result{}, err
			}
		}
		if err != nil && !errors.IsNotFound(err) {
			// Some error other than not found occurred when trying to get the secret, put the object into error state and requeue
			_ = r.handleGeneralReconcileError(ctx, accessKey, err)
			return ctrl.Result{}, err
		}
		if err := r.handleGrantDenied(ctx, accessKey); err != nil {
			// Failed to update status after grant denied, requeue and try again
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Fetch the corresponding provider config
	providerConfig := &awsaccesskeyoperatorv1alpha1.IAMProviderConfig{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Namespace: accessKey.Spec.ProviderConfigRef.Namespace,
		Name:      accessKey.Spec.ProviderConfigRef.Name,
	}, providerConfig)

	if errors.IsNotFound(err) {
		if err := r.handleProviderConfigNotFound(ctx, accessKey); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if err != nil {
		if err := r.handleGeneralReconcileError(ctx, accessKey, err); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Check if the access key secret already exists and has a valid key
	secretExists, secretConflict, err := r.accessKeySecretExistsAndHasValidKey(ctx, accessKey, providerConfig)
	if err != nil {
		_ = r.handleGeneralReconcileError(ctx, accessKey, err)
		return ctrl.Result{}, err
	}

	if secretConflict {
		if err := r.handleSecretConflict(ctx, accessKey); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if secretExists {
		// Nothing to do here
		if err := r.setAccessKeyReady(ctx, accessKey, "AlreadyExists", "Access key already exists and is valid in secret"); err != nil {
			// The access key is ready here, but we couldn't update it's satus. Return the error and try again the next reconcile
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// Create a new access key and store it in the specified secret
	err = r.createAccessKeyAndStoreInSecret(ctx, accessKey, providerConfig)
	if err != nil {
		_ = r.handleGeneralReconcileError(ctx, accessKey, err)
		return ctrl.Result{}, err
	}

	if err := r.setAccessKeyReady(ctx, accessKey, "ReconcileSuccess", "Access key successfully created and stored in secret"); err != nil {
		// The access key is ready here, but we couldn't update it's satus. Return the error and try again the next reconcile
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *IAMAccessKeyReconciler) createAccessKeyAndStoreInSecret(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig) error {
	adminConfig, err := r.getAdminConfig(ctx, providerConfig)
	if err != nil {
		return fmt.Errorf("failed to get admin config: %w", err)
	}

	iamClient := iam.NewFromConfig(adminConfig)

	err = r.clearAccessKeysForuser(ctx, accessKey, iamClient)
	if err != nil {
		return fmt.Errorf("failed to clear existing access keys for user: %w", err)
	}

	createOutput, err := iamClient.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{
		UserName: &accessKey.Spec.Username,
	})

	if err != nil {
		return fmt.Errorf("failed to create access key for user %s: %w", accessKey.Spec.Username, err)
	}

	// Store the new access key in the specified Kubernetes Secret
	secretData := fmt.Sprintf("[default]\naws_access_key_id=%s\naws_secret_access_key=%s\n", *createOutput.AccessKey.AccessKeyId, *createOutput.AccessKey.SecretAccessKey)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      accessKey.Spec.SecretName,
			Namespace: accessKey.Namespace,
		},
		Data: map[string][]byte{
			accessKey.Spec.SecretField: []byte(secretData),
		},
	}

	if err := ctrl.SetControllerReference(accessKey, secret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference on secret: %w", err)
	}

	err = r.Client.Create(ctx, secret)
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	return nil
}

func (r *IAMAccessKeyReconciler) clearAccessKeysForuser(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, client *iam.Client) error {
	// List existing access keys for the user
	listOutput, err := client.ListAccessKeys(ctx, &iam.ListAccessKeysInput{
		UserName: &accessKey.Spec.Username,
	})

	if err != nil {
		return fmt.Errorf("failed to list access keys for user %s: %w", accessKey.Spec.Username, err)
	}

	// Delete each existing access key
	for _, metadata := range listOutput.AccessKeyMetadata {
		_, err := client.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
			UserName:    &accessKey.Spec.Username,
			AccessKeyId: metadata.AccessKeyId,
		})

		if err != nil {
			return fmt.Errorf("failed to delete access key %s for user %s: %w", *metadata.AccessKeyId, accessKey.Spec.Username, err)
		}
	}

	return nil
}

func (r *IAMAccessKeyReconciler) getAdminConfig(ctx context.Context, providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig) (aws.Config, error) {
	// get the admin secret from the provider config ref
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: providerConfig.Namespace,
		Name:      providerConfig.Spec.AdminCredentialsSecretRef.Name,
	}, secret)

	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to get admin secret: %w", err)
	}

	secretData, exists := secret.Data[providerConfig.Spec.AdminCredentialsSecretKey]
	if !exists {
		return aws.Config{}, fmt.Errorf("admin secret is missing required key: %s", providerConfig.Spec.AdminCredentialsSecretKey)
	}

	awsConfig, err := loadAWSConfigFromString(ctx, string(secretData), providerConfig)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load AWS config from admin secret: %w", err)
	}

	return awsConfig, nil

}

// accessKeySecretExistsAndHasValidKey returns (exists, conflict, error).
// conflict is true when the secret exists but is not owned by this IAMAccessKey,
// preventing recreation. In that case exists is always false.
func (r *IAMAccessKeyReconciler) accessKeySecretExistsAndHasValidKey(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig) (bool, bool, error) {

	log := logf.FromContext(ctx)
	// Check if the secret already exists
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: accessKey.Namespace,
		Name:      accessKey.Spec.SecretName,
	}, secret)

	if errors.IsNotFound(err) {
		return false, false, nil
	}

	if err != nil {
		return false, false, fmt.Errorf("failed to get secret: %w", err)
	}

	// Check whether we have a valid access key in the secret
	secretData, exists := secret.Data[accessKey.Spec.SecretField]
	if !exists {
		if !isOwnedByAccessKey(secret, accessKey) {
			return false, true, nil
		}
		log.Info(fmt.Sprintf("secret %s/%s exists but is missing required key %s, will be reissued", accessKey.Namespace, accessKey.Spec.SecretName, accessKey.Spec.SecretField))
		err = r.deleteSecret(ctx, secret)
		if err != nil {
			return false, false, fmt.Errorf("failed to delete invalid secret: %w", err)
		}
		return false, false, nil
	}

	awsConfig, err := loadAWSConfigFromString(ctx, string(secretData), providerConfig)
	if err != nil {
		if !isOwnedByAccessKey(secret, accessKey) {
			return false, true, nil
		}
		log.Info(fmt.Sprintf("secret %s/%s exists but does not contain valid AWS credentials, will be reissued: %v", accessKey.Namespace, accessKey.Spec.SecretName, err))
		err = r.deleteSecret(ctx, secret)
		if err != nil {
			return false, false, fmt.Errorf("failed to delete invalid secret: %w", err)
		}
		return false, false, nil
	}

	stsClient := sts.NewFromConfig(awsConfig)
	_, err = stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		if !isOwnedByAccessKey(secret, accessKey) {
			return false, true, nil
		}
		log.Info(fmt.Sprintf("secret %s/%s exists and loads but failed validation, will be reissued: %v", accessKey.Namespace, accessKey.Spec.SecretName, err))
		err = r.deleteSecret(ctx, secret)
		if err != nil {
			return false, false, fmt.Errorf("failed to delete invalid secret: %w", err)
		}
		return false, false, nil
	}

	// key is valid!
	return true, false, nil

}

func (r *IAMAccessKeyReconciler) deleteSecret(ctx context.Context, secret *corev1.Secret) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("deleting stale secret %s/%s", secret.Namespace, secret.Name))
	err := r.Client.Delete(ctx, secret)
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	return nil
}

func loadAWSConfigFromString(ctx context.Context, configString string, providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig) (aws.Config, error) {
	iniData, err := ini.Load([]byte(configString))
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to parse AWS config: %w", err)
	}

	section, err := iniData.GetSection("default")
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to get default section from AWS config: %w", err)
	}

	accessKeyID := section.Key("aws_access_key_id").String()
	secretAccessKey := section.Key("aws_secret_access_key").String()

	if accessKeyID == "" || secretAccessKey == "" {
		return aws.Config{}, fmt.Errorf("AWS config is missing required keys")
	}

	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(providerConfig.Spec.Region),
		config.WithBaseEndpoint(providerConfig.Spec.Endpoint),
		config.WithCredentialsProvider(
			aws.NewCredentialsCache(
				credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
			),
		),
	)

	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return cfg, nil
}

// isPermittedByGrant checks whether an IAMProviderGrant in the same namespace as
// accessKey allows the requested providerConfigRef and username.
func (r *IAMAccessKeyReconciler) isPermittedByGrant(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey) (bool, error) {
	grantList := &awsaccesskeyoperatorv1alpha1.IAMProviderGrantList{}
	err := r.Client.List(ctx, grantList, client.InNamespace(accessKey.Namespace))
	if err != nil {
		return false, fmt.Errorf("failed to list IAMProviderGrants: %w", err)
	}

	for _, grant := range grantList.Items {
		ref := grant.Spec.ProviderConfigRef
		if ref.Name != accessKey.Spec.ProviderConfigRef.Name || ref.Namespace != accessKey.Spec.ProviderConfigRef.Namespace {
			continue
		}
		if slices.Contains(grant.Spec.AllowedUsernames, accessKey.Spec.Username) {
			return true, nil
		}
	}

	return false, nil
}

func (r *IAMAccessKeyReconciler) setAccessKeyError(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, reason, message string) error {
	meta.SetStatusCondition(&accessKey.Status.Conditions, metav1.Condition{
		Type:    awsaccesskeyoperatorv1alpha1.ConditionReady,
		Reason:  reason,
		Status:  metav1.ConditionFalse,
		Message: message,
	})

	if err := r.Status().Update(ctx, accessKey); err != nil {
		return fmt.Errorf("failed to update IAMAccessKey Status: %w", err)
	}

	return nil
}

func (r *IAMAccessKeyReconciler) setAccessKeyReady(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, reason, message string) error {
	meta.SetStatusCondition(&accessKey.Status.Conditions, metav1.Condition{
		Type:    awsaccesskeyoperatorv1alpha1.ConditionReady,
		Reason:  reason,
		Status:  metav1.ConditionTrue,
		Message: message,
	})

	if err := r.Status().Update(ctx, accessKey); err != nil {
		return fmt.Errorf("failed to update IAMAccessKey Status: %w", err)
	}

	return nil
}

func (r *IAMAccessKeyReconciler) handleGrantDenied(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey) error {
	log := logf.FromContext(ctx)
	log.Info("no IAMProviderGrant permits this provider/username combination",
		"namespace", accessKey.Namespace,
		"providerConfigRef", accessKey.Spec.ProviderConfigRef,
		"username", accessKey.Spec.Username,
	)
	if err := r.setAccessKeyError(ctx, accessKey, "GrantDenied", fmt.Sprintf("No IAMProviderGrant in namespace %s permits provider %s/%s for username %s", accessKey.Namespace, accessKey.Spec.ProviderConfigRef.Namespace, accessKey.Spec.ProviderConfigRef.Name, accessKey.Spec.Username)); err != nil {
		return fmt.Errorf("failed to update IAMAccessKey status after grant denied: %w", err)
	}

	return nil
}

func isOwnedByAccessKey(secret *corev1.Secret, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey) bool {
	for _, ref := range secret.GetOwnerReferences() {
		if ref.Kind == "IAMAccessKey" &&
			ref.APIVersion == awsaccesskeyoperatorv1alpha1.GroupVersion.String() &&
			ref.Name == accessKey.Name {
			return true
		}
	}
	return false
}

func (r *IAMAccessKeyReconciler) handleSecretConflict(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey) error {
	log := logf.FromContext(ctx)
	log.Error(
		fmt.Errorf("secret conflict"),
		"Secret with the specified name already exists and is not owned by this IAMAccessKey",
		"namespace", accessKey.Namespace,
		"secretName", accessKey.Spec.SecretName,
	)
	if err := r.setAccessKeyError(ctx, accessKey, "SecretConflict", fmt.Sprintf(
		"Secret %s/%s already exists and is not owned by this IAMAccessKey; manual intervention required",
		accessKey.Namespace, accessKey.Spec.SecretName,
	)); err != nil {
		return fmt.Errorf("failed to update IAMAccessKey status after secret conflict: %w", err)
	}
	return nil
}

// Handle undesirable conditions
func (r *IAMAccessKeyReconciler) handleProviderConfigNotFound(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey) error {
	log := logf.FromContext(ctx)
	log.Error(fmt.Errorf("provider config not found"), "Failed to get provider config", "namespace", accessKey.Spec.ProviderConfigRef.Namespace, "name", accessKey.Spec.ProviderConfigRef.Name)

	if err := r.setAccessKeyError(ctx, accessKey, "ProviderConfigNotFound", fmt.Sprintf("Provider config %s/%s not found", accessKey.Spec.ProviderConfigRef.Namespace, accessKey.Spec.ProviderConfigRef.Name)); err != nil {
		return fmt.Errorf("failed to update IAMAccessKey status after provider config not found: %w", err)
	}

	return nil
}

func (r *IAMAccessKeyReconciler) handleGeneralReconcileError(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, err error) error {
	log := logf.FromContext(ctx)
	log.Error(err, "Failed to reconcile IAMAccessKey", "namespace", accessKey.Namespace, "name", accessKey.Name)

	if err := r.setAccessKeyError(ctx, accessKey, "ReconcileError", fmt.Sprintf("Error reconciling IAMAccessKey: %v", err)); err != nil {
		return fmt.Errorf("failed to update IAMAccessKey status after reconcile error: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IAMAccessKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&awsaccesskeyoperatorv1alpha1.IAMAccessKey{}).
		Watches(
			&awsaccesskeyoperatorv1alpha1.IAMProviderConfig{},
			handler.EnqueueRequestsFromMapFunc(r.iamAccessKeysForProviderConfig),
		).
		Watches(
			&awsaccesskeyoperatorv1alpha1.IAMProviderGrant{},
			handler.EnqueueRequestsFromMapFunc(r.iamAccessKeysForProviderGrant),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.iamAccessKeysForSecret),
			builder.WithPredicates(predicate.NewPredicateFuncs(r.isRelevantSecret)),
		).
		Named("iamaccesskey").
		Complete(r)
}

// iamAccessKeysForProviderConfig maps an IAMProviderConfig to all IAMAccessKey
// objects that reference it, so they are re-reconciled when the config changes.
func (r *IAMAccessKeyReconciler) iamAccessKeysForProviderConfig(ctx context.Context, obj client.Object) []reconcile.Request {
	accessKeyList := &awsaccesskeyoperatorv1alpha1.IAMAccessKeyList{}
	if err := r.Client.List(ctx, accessKeyList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, ak := range accessKeyList.Items {
		ref := ak.Spec.ProviderConfigRef
		if ref.Name == obj.GetName() && ref.Namespace == obj.GetNamespace() {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: ak.Name, Namespace: ak.Namespace},
			})
		}
	}
	return requests
}

// iamAccessKeysForProviderGrant maps an IAMProviderGrant to all IAMAccessKey
// objects in the same namespace, since any grant change may affect their eligibility.
func (r *IAMAccessKeyReconciler) iamAccessKeysForProviderGrant(ctx context.Context, obj client.Object) []reconcile.Request {
	accessKeyList := &awsaccesskeyoperatorv1alpha1.IAMAccessKeyList{}
	if err := r.Client.List(ctx, accessKeyList, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, ak := range accessKeyList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: ak.Name, Namespace: ak.Namespace},
		})
	}
	return requests
}

// isRelevantSecret is the predicate that gates the secret watcher. It passes
// secrets that are either owned by an IAMAccessKey (output secrets) or
// referenced as admin credentials by an IAMProviderConfig in the same namespace.
func (r *IAMAccessKeyReconciler) isRelevantSecret(obj client.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "IAMAccessKey" && ref.APIVersion == awsaccesskeyoperatorv1alpha1.GroupVersion.String() {
			return true
		}
	}

	configList := &awsaccesskeyoperatorv1alpha1.IAMProviderConfigList{}
	if err := r.Client.List(context.Background(), configList, client.InNamespace(obj.GetNamespace())); err != nil {
		return false
	}
	for _, cfg := range configList.Items {
		if cfg.Spec.AdminCredentialsSecretRef.Name == obj.GetName() {
			return true
		}
	}
	return false
}

// iamAccessKeysForSecret maps a secret to the IAMAccessKey objects that should
// be re-reconciled when it changes. It handles two cases:
//   - Output secrets: the secret is owned by an IAMAccessKey → enqueue that key.
//   - Admin credential secrets: the secret is referenced by an IAMProviderConfig
//     → enqueue all IAMAccessKey objects that reference that config.
func (r *IAMAccessKeyReconciler) iamAccessKeysForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	var requests []reconcile.Request

	// Case 1: secret is owned by an IAMAccessKey.
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "IAMAccessKey" && ref.APIVersion == awsaccesskeyoperatorv1alpha1.GroupVersion.String() {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: ref.Name, Namespace: obj.GetNamespace()},
			})
		}
	}

	// Case 2: secret is an admin credentials secret for an IAMProviderConfig.
	configList := &awsaccesskeyoperatorv1alpha1.IAMProviderConfigList{}
	if err := r.Client.List(ctx, configList, client.InNamespace(obj.GetNamespace())); err != nil {
		return requests
	}
	for _, cfg := range configList.Items {
		if cfg.Spec.AdminCredentialsSecretRef.Name != obj.GetName() {
			continue
		}
		accessKeyList := &awsaccesskeyoperatorv1alpha1.IAMAccessKeyList{}
		if err := r.Client.List(ctx, accessKeyList); err != nil {
			continue
		}
		for _, ak := range accessKeyList.Items {
			ref := ak.Spec.ProviderConfigRef
			if ref.Name == cfg.Name && ref.Namespace == cfg.Namespace {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: ak.Name, Namespace: ak.Namespace},
				})
			}
		}
	}

	return requests
}
