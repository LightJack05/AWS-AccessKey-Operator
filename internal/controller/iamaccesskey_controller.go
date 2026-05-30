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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
		r.handleGeneralReconcileError(ctx, accessKey, err)
		return ctrl.Result{}, err
	}
	if !permitted {
		r.handleGrantDenied(ctx, accessKey)
		return ctrl.Result{}, nil
	}

	// Fetch the corresponding provider config
	providerConfig := &awsaccesskeyoperatorv1alpha1.IAMProviderConfig{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Namespace: accessKey.Spec.ProviderConfigRef.Namespace,
		Name:      accessKey.Spec.ProviderConfigRef.Name,
	}, providerConfig)

	if errors.IsNotFound(err) {
		r.handleProviderConfigNotFound(ctx, accessKey)
		return ctrl.Result{}, err
	}

	if err != nil {
		r.handleGeneralReconcileError(ctx, accessKey, err)
		return ctrl.Result{}, err
	}

	// Check if the access key secret already exists and has a valid key
	secretExists, err := r.accessKeySecretExistsAndHasValidKey(ctx, accessKey, providerConfig)
	if err != nil {
		r.handleGeneralReconcileError(ctx, accessKey, err)
		return ctrl.Result{}, err
	}

	if secretExists {
		// Nothing to do here
		return ctrl.Result{}, nil
	}

	// Create a new access key and store it in the specified secret
	err = r.createAccessKeyAndStoreInSecret(ctx, accessKey, providerConfig)
	if err != nil {
		r.handleGeneralReconcileError(ctx, accessKey, err)
		return ctrl.Result{}, err
	}

	// Update the status of the IAMAccessKey to reflect successful creation
	accessKey.Status.Healthy = true
	accessKey.Status.Message = "Access key successfully created and stored in secret"
	meta.SetStatusCondition(&accessKey.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "ReconcileSuccess",
		Message: "Access key successfully created and stored in secret",
	})

	err = r.Client.Status().Update(ctx, accessKey)
	if err != nil {
		log.Error(err, "Failed to update IAMAccessKey status")
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

	err = r.clearAccessKeysForuser(ctx, accessKey, providerConfig, iamClient)
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
			accessKey.Spec.SecretKey: []byte(secretData),
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

func (r *IAMAccessKeyReconciler) clearAccessKeysForuser(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig, client *iam.Client) error {
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

	awsConfig, err := loadAWSConfigFromString(string(secretData), providerConfig)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load AWS config from admin secret: %w", err)
	}

	return awsConfig, nil

}

func (r *IAMAccessKeyReconciler) accessKeySecretExistsAndHasValidKey(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig) (bool, error) {

	log := logf.FromContext(ctx)
	// Check if the secret already exists
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: accessKey.Namespace,
		Name:      accessKey.Spec.SecretName,
	}, secret)

	if errors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to get secret: %w", err)
	}

	// Check whether we have a valid access key in the secret
	secretData, exists := secret.Data[accessKey.Spec.SecretKey]
	if !exists {
		log.Info(fmt.Sprintf("secret %s/%s exists but is missing required key %s, will be reissued", accessKey.Namespace, accessKey.Spec.SecretName, accessKey.Spec.SecretKey))
		err = r.deleteSecret(ctx, secret)
		if err != nil {
			return false, fmt.Errorf("failed to delete invalid secret: %w", err)
		}
		return false, nil
	}

	awsConfig, err := loadAWSConfigFromString(string(secretData), providerConfig)
	if err != nil {
		log.Info(fmt.Sprintf("secret %s/%s exists but does not contain valid AWS credentials, will be reissued: %v", accessKey.Namespace, accessKey.Spec.SecretName, err))
		err = r.deleteSecret(ctx, secret)
		if err != nil {
			return false, fmt.Errorf("failed to delete invalid secret: %w", err)
		}
		return false, nil
	}

	iamClient := iam.NewFromConfig(awsConfig)
	_, err = iamClient.GetUser(ctx, &iam.GetUserInput{UserName: &accessKey.Spec.Username})
	if err != nil {
		log.Info(fmt.Sprintf("secret %s/%s exists and loads but failed validation, will be reissued: %v", accessKey.Namespace, accessKey.Spec.SecretName, err))
		err = r.deleteSecret(ctx, secret)
		if err != nil {
			return false, fmt.Errorf("failed to delete invalid secret: %w", err)
		}
		return false, nil
	}

	// key is valid!
	return true, nil

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

func loadAWSConfigFromString(configString string, providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig) (aws.Config, error) {
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
		context.TODO(),
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
		for _, u := range grant.Spec.AllowedUsernames {
			if u == accessKey.Spec.Username {
				return true, nil
			}
		}
	}

	return false, nil
}

func (r *IAMAccessKeyReconciler) handleGrantDenied(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey) {
	log := logf.FromContext(ctx)
	log.Info("no IAMProviderGrant permits this provider/username combination",
		"namespace", accessKey.Namespace,
		"providerConfigRef", accessKey.Spec.ProviderConfigRef,
		"username", accessKey.Spec.Username,
	)

	meta.SetStatusCondition(&accessKey.Status.Conditions, metav1.Condition{
		Type:    "GrantDenied",
		Reason:  "NoMatchingGrant",
		Status:  metav1.ConditionTrue,
		Message: fmt.Sprintf("No IAMProviderGrant in namespace %s permits provider %s/%s for username %s", accessKey.Namespace, accessKey.Spec.ProviderConfigRef.Namespace, accessKey.Spec.ProviderConfigRef.Name, accessKey.Spec.Username),
	})
	accessKey.Status.Healthy = false

	if err := r.Status().Update(ctx, accessKey); err != nil {
		log.Error(err, "failed to update IAMAccessKey status after grant denial")
	}
}

// Handle undesirable conditions
func (r *IAMAccessKeyReconciler) handleProviderConfigNotFound(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey) {
	log := logf.FromContext(ctx)
	log.Error(fmt.Errorf("provider config not found"), "Failed to get provider config", "namespace", accessKey.Spec.ProviderConfigRef.Namespace, "name", accessKey.Spec.ProviderConfigRef.Name)

	// Update the status of the IAMAccessKey to reflect the error
	meta.SetStatusCondition(&accessKey.Status.Conditions, metav1.Condition{
		Type:    "ProviderError",
		Reason:  "ProviderConfigNotFound",
		Status:  metav1.ConditionFalse,
		Message: fmt.Sprintf("Provider config %s/%s not found", accessKey.Spec.ProviderConfigRef.Namespace, accessKey.Spec.ProviderConfigRef.Name),
	})
	accessKey.Status.Healthy = false

	r.Status().Update(ctx, accessKey)
}

func (r *IAMAccessKeyReconciler) handleGeneralReconcileError(ctx context.Context, accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey, err error) {
	log := logf.FromContext(ctx)
	log.Error(err, "Failed to reconcile IAMAccessKey", "namespace", accessKey.Namespace, "name", accessKey.Name)

	// Update the status of the IAMAccessKey to reflect the error
	meta.SetStatusCondition(&accessKey.Status.Conditions, metav1.Condition{
		Type:    "ReconcileError",
		Reason:  "ErrorReconciling",
		Status:  metav1.ConditionFalse,
		Message: fmt.Sprintf("Error reconciling IAMAccessKey: %v", err),
	})
	accessKey.Status.Healthy = false

	r.Status().Update(ctx, accessKey)
}

// SetupWithManager sets up the controller with the Manager.
func (r *IAMAccessKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&awsaccesskeyoperatorv1alpha1.IAMAccessKey{}).
		Named("iamaccesskey").
		Complete(r)
}
