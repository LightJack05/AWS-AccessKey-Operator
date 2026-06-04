# IAMProviderGrant

`IAMProviderGrant` is the security boundary that controls which namespaces can use a given `IAMProviderConfig` and which IAM usernames they may request.

A cluster administrator creates an `IAMProviderGrant` in a namespace to allow `IAMAccessKey` resources in that namespace to provision credentials. Without a matching grant, the operator refuses to create any access keys regardless of Kubernetes RBAC permissions on `IAMAccessKey`.

!!! warning "RBAC is the enforcement mechanism"
    The permission model for this operator hinges on Kubernetes RBAC applied to `IAMProviderGrant`. Only users with `create`/`update` permissions on `IAMProviderGrant` — typically cluster administrators — should be able to create grants. Namespace-level users should only have permissions on `IAMAccessKey`.

    A namespace owner who can also create `IAMProviderGrant` objects effectively controls which IAM users and providers that namespace can access. Ensure your RBAC policies reflect this boundary.

## Example

```yaml
apiVersion: aws-accesskey-operator.lightjack.de/v1alpha1
kind: IAMProviderGrant
metadata:
  name: allow-production-iam
  namespace: my-application
spec:
  providerConfigRef:
    name: aws-production
    namespace: aws-accesskey-operator-system
  allowedUsernames:
    - my-service-account
    - another-service-account
```

## Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `providerConfigRef.name` | string | Yes | Name of the `IAMProviderConfig` to grant access to. |
| `providerConfigRef.namespace` | string | Yes | Namespace of the `IAMProviderConfig`. |
| `allowedUsernames` | []string | Yes | List of IAM usernames that `IAMAccessKey` resources in this namespace may request. At least one entry is required. |

## How the grant is checked

When the operator reconciles an `IAMAccessKey`, it lists all `IAMProviderGrant` objects in the same namespace. It considers an `IAMAccessKey` permitted if **any** grant in the namespace:

1. References the same `IAMProviderConfig` (matching both name and namespace), **and**
2. Includes the `IAMAccessKey`'s `username` in `allowedUsernames`.

If no matching grant is found, the `IAMAccessKey` enters a `Ready=False` state with reason `GrantDenied` and any previously created credentials Secret is deleted.

## Removing a grant

Deleting or updating an `IAMProviderGrant` to remove a username triggers reconciliation of all `IAMAccessKey` resources in the namespace. Any access key whose username is no longer covered by any grant will have its credentials Secret deleted and will enter the `GrantDenied` error state.
