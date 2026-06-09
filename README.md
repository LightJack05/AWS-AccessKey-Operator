# AWS AccessKey Operator

A Kubernetes operator that manages AWS IAM access keys as native Kubernetes resources. It automatically provisions, stores, and validates IAM access keys for IAM users, writing the resulting credentials into Kubernetes Secrets in AWS INI format.

Supports both AWS IAM and any SigV4-compatible IAM implementation (e.g. [SeaweedFS](https://github.com/seaweedfs/seaweedfs)).

> [!WARNING]
> Designed exclusively for **IAM service accounts**. When creating or rotating an access key, the operator **deletes all existing access keys** for the target IAM user. Do not use this operator to manage access keys for human IAM users.

## How it works

The operator reconciles three custom resource kinds:

| Kind | Purpose |
|---|---|
| `IAMProviderConfig` | Defines an IAM endpoint and the admin credentials used to manage keys |
| `IAMProviderGrant` | Grants a namespace permission to use a provider config and specific usernames |
| `IAMAccessKey` | Requests an access key for an IAM user and stores it in a Secret |

When an `IAMAccessKey` is created, the operator verifies that a matching `IAMProviderGrant` exists in the same namespace, validates any existing credentials via `sts:GetCallerIdentity`, and — if the credentials are missing or invalid — rotates the IAM user's access keys and writes fresh credentials to the specified Kubernetes Secret.

Access is controlled through `IAMProviderGrant`: only namespaces with a grant (typically created by a cluster administrator) can provision keys for the listed IAM usernames.

## Documentation

Full documentation is available at **https://lightjack05.github.io/AWS-AccessKey-Operator/**, including:

- [Installation guide](https://lightjack05.github.io/AWS-AccessKey-Operator/installation/)
- [API reference](https://lightjack05.github.io/AWS-AccessKey-Operator/api/)

## TL;DR

Install the operator with Helm:

```bash
helm install aws-accesskey-operator \
  oci://ghcr.io/lightjack05/charts/aws-accesskey-operator \
  --namespace aws-accesskey-operator-system \
  --create-namespace
```

Create a Secret with IAM admin credentials in the operator namespace, then point an `IAMProviderConfig` at it:

```yaml
apiVersion: aws-accesskey-operator.lightjack.de/v1alpha1
kind: IAMProviderConfig
metadata:
  name: aws-production
  namespace: aws-accesskey-operator-system
spec:
  endpoint: "https://iam.amazonaws.com"
  region: "us-east-1"
  adminCredentialsSecretRef:
    name: iam-admin-credentials
  adminCredentialsSecretKey: credentials
```

Grant a namespace access to the provider with an `IAMProviderGrant` (admin-only — this is the security boundary):

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
```

Request an access key with an `IAMAccessKey`:

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

The operator writes the credentials to the Secret `my-application/my-aws-credentials` under the key `credentials` in AWS INI format, ready to be mounted into your workloads.

## License

[MIT](LICENSE)
