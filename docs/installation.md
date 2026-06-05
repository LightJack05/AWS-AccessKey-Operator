# Installation

The operator is distributed as a Helm chart published to the GitHub Container Registry OCI registry.

## Prerequisites

- Kubernetes 1.25+
- Helm 3.8+ (for OCI registry support)
- An IAM user with permissions to create, list, and delete access keys for the target service accounts

## Installing with Helm

### 1. Install the chart

The chart is hosted at `oci://ghcr.io/lightjack05/charts/aws-accesskey-operator`. Install it into a dedicated namespace:

```bash
helm install aws-accesskey-operator \
  oci://ghcr.io/lightjack05/charts/aws-accesskey-operator \
  --namespace aws-accesskey-operator-system \
  --create-namespace
```

To pin a specific version:

```bash
helm install aws-accesskey-operator \
  oci://ghcr.io/lightjack05/charts/aws-accesskey-operator \
  --version <version> \
  --namespace aws-accesskey-operator-system \
  --create-namespace
```

By default the chart installs the CRDs, the controller manager deployment, and the required RBAC resources.

### 2. Create the admin credentials Secret

The operator needs an IAM admin credential stored in a Kubernetes Secret within the **operator namespace**. The Secret must contain credentials in [AWS INI format](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html) under a key of your choice:

```bash
kubectl create secret generic iam-admin-credentials \
  --namespace aws-accesskey-operator-system \
  --from-literal=credentials="[default]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE
aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
```

The admin IAM user must have at minimum the following IAM permissions for each service account user it manages:

```json
{
  "Effect": "Allow",
  "Action": [
    "iam:CreateAccessKey",
    "iam:DeleteAccessKey",
    "iam:ListAccessKeys"
  ],
  "Resource": "arn:aws:iam::<account-id>:user/<service-account-username>"
}
```

### 3. Create an IAMProviderConfig

Create an `IAMProviderConfig` in the operator namespace, referencing the admin credentials Secret:

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

For a SeaweedFS-compatible endpoint, set `endpoint` to the SeaweedFS filer address and choose any value for `region`:

```yaml
spec:
  endpoint: "http://seaweedfs-filer:8111"
  region: "us-east-1"
  adminCredentialsSecretRef:
    name: seaweedfs-admin-credentials
  adminCredentialsSecretKey: credentials
```

### 4. Grant namespace access with IAMProviderGrant

!!! warning "Admin-only operation"
    `IAMProviderGrant` is the security boundary for this operator. Only cluster administrators should have write access to `IAMProviderGrant` resources. Granting a namespace access to a provider allows anyone in that namespace who can create `IAMAccessKey` resources to provision AWS credentials for the listed IAM usernames.

Create an `IAMProviderGrant` in each namespace that needs to create access keys. The grant must be placed in the **same namespace** as the `IAMAccessKey` resources that will use it:

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

### 5. Create an IAMAccessKey

With the grant in place, create an `IAMAccessKey` in the same namespace:

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

The operator will create (or validate) the access key and write the credentials to the Secret `my-application/my-aws-credentials` under the key `credentials` in AWS INI format:

```ini
[default]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE
aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

Check the status of the resource:

```bash
kubectl get iamaccesskey my-service-account-key -n my-application
```

```
NAME                       READY   AGE
my-service-account-key     True    30s
```

## Helm chart values

The following table lists the configurable values for the Helm chart.

| Value | Default | Description |
|---|---|---|
| `manager.replicas` | `1` | Number of controller manager replicas |
| `manager.image.repository` | `ghcr.io/lightjack05/aws-accesskey-operator` | Controller image repository |
| `manager.image.tag` | `latest` | Controller image tag |
| `manager.image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `manager.args` | `["--leader-elect"]` | Extra arguments passed to the manager binary |
| `manager.env` | `[]` | Extra environment variables |
| `manager.resources.limits.cpu` | `500m` | CPU limit |
| `manager.resources.limits.memory` | `128Mi` | Memory limit |
| `manager.resources.requests.cpu` | `10m` | CPU request |
| `manager.resources.requests.memory` | `64Mi` | Memory request |
| `manager.affinity` | `{}` | Pod affinity rules |
| `manager.nodeSelector` | `{}` | Node selector |
| `manager.tolerations` | `[]` | Pod tolerations |
| `manager.imagePullSecrets` | `[]` | Image pull secrets |
| `rbacHelpers.enable` | `false` | Install convenience admin/editor/viewer ClusterRoles for CRDs |
| `crd.enable` | `true` | Install CRDs with the chart |
| `crd.keep` | `true` | Keep CRDs when the chart is uninstalled |
| `metrics.enable` | `true` | Expose the `/metrics` endpoint |
| `metrics.port` | `8443` | Metrics server port |
| `certManager.enable` | `false` | Use cert-manager for TLS certificates |
| `prometheus.enable` | `false` | Install a Prometheus `ServiceMonitor` |

### Example: custom image tag

```bash
helm install aws-accesskey-operator \
  oci://ghcr.io/lightjack05/charts/aws-accesskey-operator \
  --namespace aws-accesskey-operator-system \
  --create-namespace \
  --set manager.image.tag=v1.2.3
```

### Example: enable Prometheus monitoring

```bash
helm install aws-accesskey-operator \
  oci://ghcr.io/lightjack05/charts/aws-accesskey-operator \
  --namespace aws-accesskey-operator-system \
  --create-namespace \
  --set prometheus.enable=true \
  --set certManager.enable=true
```

## Upgrading

```bash
helm upgrade aws-accesskey-operator \
  oci://ghcr.io/lightjack05/charts/aws-accesskey-operator \
  --namespace aws-accesskey-operator-system
```

## Uninstalling

```bash
helm uninstall aws-accesskey-operator --namespace aws-accesskey-operator-system
```

Note: By default (`crd.keep=true`) the CRDs are **not** removed when the chart is uninstalled. To also remove the CRDs, set `crd.keep=false` before uninstalling or delete them manually:

```bash
kubectl delete crd \
  iamaccesskeys.aws-accesskey-operator.lightjack.de \
  iamproviderconfigs.aws-accesskey-operator.lightjack.de \
  iamprovidergrants.aws-accesskey-operator.lightjack.de
```
