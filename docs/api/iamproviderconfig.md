# IAMProviderConfig

`IAMProviderConfig` defines an IAM-compatible API endpoint and the administrator credentials the operator uses to create, list, and delete access keys on behalf of `IAMAccessKey` resources.

An `IAMProviderConfig` is namespaced and is typically placed in the operator namespace. It is referenced by `IAMProviderGrant` and `IAMAccessKey` resources via a cross-namespace reference.

## Example

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

## Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `endpoint` | string | Yes | Base URL of the IAM API. Use `https://iam.amazonaws.com` for AWS, or the address of any SigV4-compatible endpoint (e.g. SeaweedFS). Must begin with `http://` or `https://`. |
| `region` | string | Yes | AWS region used for SigV4 request signing. For non-AWS endpoints this is still required; any non-empty string is valid (e.g. `us-east-1`). |
| `adminCredentialsSecretRef.name` | string | Yes | Name of the Kubernetes Secret in the **operator namespace** that holds the admin credentials. |
| `adminCredentialsSecretKey` | string | Yes | Key within the Secret whose value contains the admin credentials in [AWS INI format](#credentials-format). |

### Credentials format

The Secret value referenced by `adminCredentialsSecretKey` must contain valid credentials in AWS INI format:

```ini
[default]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE
aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

## Reactive reconciliation

Any change to the admin credentials Secret causes all `IAMAccessKey` resources that reference this `IAMProviderConfig` to be re-reconciled automatically.
