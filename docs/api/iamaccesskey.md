# IAMAccessKey

`IAMAccessKey` requests an AWS IAM access key for a named IAM user and stores the resulting credentials in a Kubernetes Secret.

!!! danger "For service accounts only"
    When the operator needs to create or rotate credentials, it **deletes all existing access keys** for the target IAM user before issuing a new one. This ensures only one active key exists at any time, but it means **any pre-existing access keys for that user will be permanently deleted**.

    This operator is designed exclusively for IAM service accounts — dedicated programmatic users that are not shared with humans or other systems. Do not use it to manage access keys for human users or shared IAM users.

## Prerequisites

Before creating an `IAMAccessKey`, ensure that:

1. An [`IAMProviderConfig`](iamproviderconfig.md) exists that points to the target IAM endpoint.
2. An [`IAMProviderGrant`](iamprovidergrant.md) exists in the **same namespace** as the `IAMAccessKey`, referencing the provider config and listing the target IAM username in `allowedUsernames`.

## Example

```yaml
apiVersion: aws-accesskey-operator.lightjack.de/v1alpha1
kind: IAMAccessKey
metadata:
  name: my-service-account-key
  namespace: my-application
spec:
  providerConfigRef:
    name: aws-production
    namespace: aws-accesskey-operator-system
  username: my-service-account
  secretName: my-aws-credentials
  secretField: credentials
```

The operator creates (or validates) the access key and writes the credentials to `my-application/my-aws-credentials` under the key `credentials`:

```ini
[default]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE
aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

## Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `providerConfigRef.name` | string | Yes | Name of the `IAMProviderConfig` to use. |
| `providerConfigRef.namespace` | string | Yes | Namespace of the `IAMProviderConfig`. |
| `username` | string | Yes | IAM username whose access key will be managed. |
| `secretName` | string | Yes | Name of the Kubernetes Secret to create in the same namespace as this resource. |
| `secretField` | string | Yes | Key within the Secret where the credentials will be stored in AWS INI format. |

## Status

### Conditions

`IAMAccessKey` uses a single condition type:

| Condition type | Description |
|---|---|
| `Ready` | Whether the access key has been successfully provisioned and the credentials Secret is valid |

### Ready condition reasons

| Reason | Status | Description |
|---|---|---|
| `ReconcileSuccess` | `True` | The access key was just created and stored successfully |
| `AlreadyExists` | `True` | A valid credentials Secret already exists; no action was taken |
| `GrantDenied` | `False` | No `IAMProviderGrant` in this namespace permits the requested provider and username |
| `ProviderConfigNotFound` | `False` | The referenced `IAMProviderConfig` does not exist |
| `SecretConflict` | `False` | A Secret with the specified name already exists but is not owned by this `IAMAccessKey`; manual intervention is required |
| `ReconcileError` | `False` | An unexpected error occurred during reconciliation (check operator logs for details) |

### Checking status

```bash
kubectl get iamaccesskey my-service-account-key -n my-application
```

```
NAME                       READY   AGE
my-service-account-key     True    2m
```

```bash
kubectl describe iamaccesskey my-service-account-key -n my-application
```

## Secret ownership

The operator sets itself as the owner of the credentials Secret via a Kubernetes owner reference. This means:

- If the Secret is deleted, the operator will detect the change and re-create it automatically.
- If the `IAMAccessKey` resource is deleted, Kubernetes garbage-collects the owned Secret.
- If a Secret with the specified name already exists and is **not** owned by this `IAMAccessKey`, the operator will not overwrite it. Instead, it sets `Ready=False` with reason `SecretConflict` and waits for manual resolution.

## Key rotation

To rotate credentials, delete the credentials Secret. The operator detects the deletion, clears all existing access keys for the IAM user, and issues a new one.

```bash
kubectl delete secret my-aws-credentials -n my-application
```

## Reactive reconciliation

The operator re-reconciles an `IAMAccessKey` when any of the following change:

- The `IAMAccessKey` resource itself
- The `IAMProviderConfig` it references
- Any `IAMProviderGrant` in the same namespace
- The credentials Secret (whether output or admin credentials)
