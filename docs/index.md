# AWS AccessKey Operator

The AWS AccessKey Operator is a Kubernetes operator that manages AWS IAM access keys as native Kubernetes resources. It automatically provisions, stores, and validates IAM access keys for IAM users, writing the resulting credentials into Kubernetes Secrets in AWS INI format.

The operator supports both AWS IAM and any SigV4-compatible IAM implementation such as [SeaweedFS](https://github.com/seaweedfs/seaweedfs).

---

!!! danger "For service accounts only"
    This operator is designed exclusively for **IAM service accounts**. When creating or rotating an access key, the operator **deletes all existing access keys** for the target IAM user before issuing a new one. Do not use this operator to manage access keys for human IAM users.

---

## How it works

The operator reconciles three custom resource kinds:

| Kind | Scope | Purpose |
|---|---|---|
| [`IAMProviderConfig`](api/iamproviderconfig.md) | Namespaced | Defines an IAM endpoint and the admin credentials used to manage keys |
| [`IAMProviderGrant`](api/iamprovidergrant.md) | Namespaced | Grants a namespace permission to use a provider config and specific usernames |
| [`IAMAccessKey`](api/iamaccesskey.md) | Namespaced | Requests an access key for an IAM user and stores it in a Secret |

### Reconciliation flow

1. A user creates an `IAMAccessKey` in their namespace, referencing an `IAMProviderConfig` and specifying an IAM username.
2. The operator checks whether an `IAMProviderGrant` exists in the same namespace that permits the referenced provider and username. If no grant is found, reconciliation stops and the resource enters a `Ready=False` state with reason `GrantDenied`.
3. If a grant is found, the operator checks whether a valid secret already exists by loading the credentials and calling `sts:GetCallerIdentity` to verify them.
4. If the secret is missing or invalid, the operator **deletes all existing access keys** for the IAM user and creates a new one, writing the credentials to the specified Kubernetes Secret.

### Permission model

Access to an `IAMProviderConfig` is controlled via RBAC on `IAMProviderGrant`. Only a user with write access to `IAMProviderGrant` resources (typically a cluster administrator) can create a grant. Without a matching grant in the same namespace, no `IAMAccessKey` resource can use a provider or create credentials, regardless of Kubernetes RBAC permissions on `IAMAccessKey` itself.

This means the permission to create access keys for a given provider and set of IAM usernames is entirely controlled by who can create `IAMProviderGrant` objects — not by who can create `IAMAccessKey` objects.

## Quick start

See the [Installation guide](installation.md) for full details. At a high level:

1. Install the operator with Helm.
2. Create an admin credentials Secret in the operator namespace.
3. Create an `IAMProviderConfig` referencing that Secret.
4. Create an `IAMProviderGrant` in each namespace that needs access keys.
5. Create `IAMAccessKey` resources to provision credentials.
