# AWS AccessKey Operator

> [!CAUTION]
> Work in progress project. Do not use yet please!

A Kubernetes operator that manages AWS IAM access keys as native Kubernetes resources. It automatically provisions, stores, and validates IAM access keys for IAM users, writing the resulting credentials into Kubernetes Secrets in AWS INI format.

Supports both AWS IAM and any SigV4-compatible IAM implementation (e.g. [SeaweedFS](https://github.com/seaweedfs/seaweedfs)).

> [!WARNING]
> Designed exclusively for **IAM service accounts**. When creating or rotating an access key, the operator **deletes all existing access keys** for the target IAM user. Do not use this operator to manage access keys for human IAM users.

## Documentation

Full documentation is available at **https://lightjack05.github.io/AWS-AccessKey-Operator/**, including:

- [Installation guide](https://lightjack05.github.io/AWS-AccessKey-Operator/installation/)
- [API reference](https://lightjack05.github.io/AWS-AccessKey-Operator/api/)

## License

[MIT](LICENSE)
