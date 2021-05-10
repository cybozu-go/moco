# Security considerations

## gRPC API

[moco-agent][], a sidecar container in mysqld Pod, provides gRPC API to
execute `CLONE INSTANCE` and required operations after CLONE.
More importantly, the request contains credentials to access the source
database.

To protect the credentials and prevent abuse of API, MOCO configures mTLS
between moco-agent and moco-controller as follows:

1. Create an [Issuer][] resource in `moco-system` namespace as the Certificate Authority.
2. Create a [Certificate][] resource to issue the certificate for `moco-controller`.
3. `moco-controller` issues certificates for each MySQLCluster by creating [Certificate][] resources.
4. `moco-controller` copies Secret resources created by cert-manager to the namespaces of MySQLCluster.
5. Both moco-controller and moco-agent verifies the certificate with the CA certificate.
    - The CA certificate is embedded in the Secret resources.
6. moco-agent additionally verifies the certificate from `moco-controller` if it's Common Name is `moco-controller`.

## MySQL passwords

MOCO generates its user passwords randomly with the OS random device.
The passwords then stored as Secret resources.

As to communication between moco-controller and mysqld, it is not (yet) over TLS.
That said, the password is encrypted anyway thanks to [caching_sha2_password](https://dev.mysql.com/doc/refman/8.0/en/caching-sha2-pluggable-authentication.html) authentication.

[moco-agent]: https://github.com/cybozu-go/moco-agent
[Issuer]: https://cert-manager.io/docs/reference/api-docs/#cert-manager.io/v1.Issuer
[Certificate]: https://cert-manager.io/docs/reference/api-docs/#cert-manager.io/v1.Certificate
