# API Reference

All custom resources belong to the API group `aws-accesskey-operator.lightjack.de`, version `v1alpha1`.

## Resource types

| Kind | Plural | Scope | Description |
|---|---|---|---|
| [`IAMProviderConfig`](iamproviderconfig.md) | `iamproviderconfigs` | Namespaced | Defines an IAM-compatible endpoint and the admin credentials the operator uses to manage access keys |
| [`IAMProviderGrant`](iamprovidergrant.md) | `iamprovidergrants` | Namespaced | Grants a namespace the right to use a specific `IAMProviderConfig` and a set of IAM usernames |
| [`IAMAccessKey`](iamaccesskey.md) | `iamaccesskeys` | Namespaced | Requests an access key for a named IAM user and stores it in a Kubernetes Secret |

## Dependency order

The three kinds must be created in order:

```
IAMProviderConfig  ←  IAMProviderGrant  ←  IAMAccessKey
```

1. An `IAMProviderConfig` defines *how* to talk to the IAM API.
2. An `IAMProviderGrant` placed in a namespace declares *which* provider and *which* IAM usernames are allowed there.
3. An `IAMAccessKey` in the same namespace as its `IAMProviderGrant` triggers key creation.

## Common fields

### Conditions

`IAMAccessKey` and `IAMProviderConfig` report status via standard Kubernetes conditions (`metav1.Condition`).

Each condition has:

| Field | Description |
|---|---|
| `type` | The condition type (e.g. `Ready`) |
| `status` | `True`, `False`, or `Unknown` |
| `reason` | A CamelCase programmatic reason for the current status |
| `message` | A human-readable description |
| `lastTransitionTime` | When the condition last changed |

### IAMProviderConfigRef

Both `IAMAccessKey` and `IAMProviderGrant` use this embedded type to reference an `IAMProviderConfig`:

| Field | Type | Description |
|---|---|---|
| `name` | string | Name of the `IAMProviderConfig` |
| `namespace` | string | Namespace of the `IAMProviderConfig` |
