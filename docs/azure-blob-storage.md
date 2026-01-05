# Using Azure Blob Storage for Backups

MOCO supports Azure Blob Storage as a backup destination alongside S3 and Google Cloud Storage.

## Prerequisites

1. An Azure Storage Account
2. A container created in the storage account
3. Proper authentication credentials configured

## Authentication

Azure Blob Storage authentication uses Azure's DefaultAzureCredential, which supports multiple authentication methods in the following order:

1. **Environment Variables**:
   ```bash
   AZURE_TENANT_ID=<your-tenant-id>
   AZURE_CLIENT_ID=<your-client-id>
   AZURE_CLIENT_SECRET=<your-client-secret>
   AZURE_STORAGE_ACCOUNT=<your-storage-account-name>
   ```

2. **Managed Identity**: Automatically used when running in Azure (AKS, Azure VMs)

3. **Azure CLI**: Uses credentials from `az login` when available

4. **Service Principal**: Can be configured through environment variables or workload identity

## Configuration

### BackupPolicy Configuration

To use Azure Blob Storage, set `backendType: azure` in your BackupPolicy:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: BackupPolicy
metadata:
  namespace: backup
  name: daily
spec:
  schedule: "0 2 * * *"
  jobConfig:
    serviceAccountName: backup-sa
    bucketConfig:
      backendType: azure                    # Set backend type to azure
      bucketName: moco-backups             # Azure container name
      endpointURL: https://myaccount.blob.core.windows.net/  # Optional: custom endpoint
    workVolume:
      emptyDir: {}
```

### MySQLCluster Configuration

Reference the BackupPolicy from your MySQLCluster:

```yaml
apiVersion: moco.cybozu.com/v1beta2
kind: MySQLCluster
metadata:
  namespace: mysql
  name: my-cluster
spec:
  replicas: 3
  backupPolicyName: daily  # References the BackupPolicy above
  # ... other configuration
```

## Setting Up Authentication

### Option 1: Using Workload Identity (Recommended for AKS)

1. Create a managed identity and assign it the "Storage Blob Data Contributor" role:
   ```bash
   az identity create --name moco-backup-identity --resource-group <rg-name>
   az role assignment create \
     --assignee <managed-identity-client-id> \
     --role "Storage Blob Data Contributor" \
     --scope /subscriptions/<subscription-id>/resourceGroups/<rg-name>/providers/Microsoft.Storage/storageAccounts/<account-name>
   ```

2. Set up federated identity credentials for your service account
3. Annotate your Kubernetes service account:
   ```yaml
   apiVersion: v1
   kind: ServiceAccount
   metadata:
     name: backup-sa
     namespace: backup
     annotations:
       azure.workload.identity/client-id: <managed-identity-client-id>
   ```

### Option 2: Using Service Principal with Kubernetes Secrets

1. Create a service principal:
   ```bash
   az ad sp create-for-rbac --name moco-backup-sp
   ```

2. Assign the "Storage Blob Data Contributor" role:
   ```bash
   az role assignment create \
     --assignee <sp-client-id> \
     --role "Storage Blob Data Contributor" \
     --scope /subscriptions/<subscription-id>/resourceGroups/<rg-name>/providers/Microsoft.Storage/storageAccounts/<account-name>
   ```

3. Create a Kubernetes secret with the credentials:
   ```yaml
   apiVersion: v1
   kind: Secret
   metadata:
     name: azure-credentials
     namespace: backup
   stringData:
     AZURE_TENANT_ID: <tenant-id>
     AZURE_CLIENT_ID: <client-id>
     AZURE_CLIENT_SECRET: <client-secret>
     AZURE_STORAGE_ACCOUNT: <storage-account-name>
   ```

4. Reference the secret in your BackupPolicy:
   ```yaml
   spec:
     jobConfig:
       envFrom:
       - secretRef:
           name: azure-credentials
       bucketConfig:
         backendType: azure
         bucketName: moco-backups
   ```

### Option 3: Using Connection String (Not Recommended for Production)

For testing purposes, you can use a connection string:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: azure-connection
  namespace: backup
stringData:
  AZURE_STORAGE_CONNECTION_STRING: "DefaultEndpointsProtocol=https;AccountName=...;AccountKey=...;EndpointSuffix=core.windows.net"
```

### Option 4: Using Azurite for Local Development

[Azurite](https://github.com/Azure/Azurite) is Microsoft's official Azure Storage Emulator for local development and testing.

#### Setup

1. **Start Azurite using Docker:**
   ```bash
   docker run -d -p 10000:10000 \
     --name azurite \
     mcr.microsoft.com/azure-storage/azurite \
     azurite-blob --blobHost 0.0.0.0 --blobPort 10000
   ```

2. **Create a container using Azure CLI:**
   ```bash
   # Azurite uses well-known credentials
   export AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;"

   az storage container create --name moco-backups --connection-string "$AZURE_STORAGE_CONNECTION_STRING"
   ```

3. **Run backups against Azurite:**
   ```bash
   # The connection string will be automatically detected
   export MYSQL_PASSWORD=your-password
   moco-backup backup --backend-type=azure moco-backups namespace cluster-name
   ```

#### Azurite Well-Known Credentials

Azurite uses these fixed credentials for local development:
- **Account Name:** `devstoreaccount1`
- **Account Key:** `Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==`
- **Blob Endpoint:** `http://127.0.0.1:10000/devstoreaccount1`

#### Using Azurite in Kubernetes/BackupPolicy

Create a Secret with the connection string:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: azurite-credentials
  namespace: backup
stringData:
  AZURE_STORAGE_CONNECTION_STRING: "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;BlobEndpoint=http://azurite:10000/devstoreaccount1;"
```

Reference it in your BackupPolicy:

```yaml
spec:
  jobConfig:
    envFrom:
    - secretRef:
        name: azurite-credentials
    bucketConfig:
      backendType: azure
      bucketName: moco-backups
```

**Note:** Azurite is for development/testing only. Never use it in production.

## Command-Line Usage

You can also use the `moco-backup` command directly with Azure:

```bash
moco-backup backup \
  --backend-type=azure \
  --endpoint=https://myaccount.blob.core.windows.net/ \
  my-container namespace cluster-name
```

Required environment variables:
- `MYSQL_PASSWORD`: MySQL password for backup user
- `AZURE_STORAGE_ACCOUNT`: Storage account name (if endpoint not specified)
- Authentication variables (AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET)

## Restore from Azure Blob Storage

Restore operations work the same way as backups:

```bash
moco-backup restore \
  --backend-type=azure \
  --endpoint=https://myaccount.blob.core.windows.net/ \
  my-container source-namespace source-cluster target-namespace target-cluster
```

## Troubleshooting

### Authentication Errors

If you see authentication errors:

1. Verify your service principal/managed identity has the correct role assignments
2. Check that environment variables are properly set
3. Ensure the RBAC permissions are propagated (can take a few minutes)

### Endpoint URL

The endpoint URL should be in the format:
- Standard: `https://<account-name>.blob.core.windows.net/`
- Custom domain: `https://custom-domain.com/`
- Azurite (local testing): `http://127.0.0.1:10000/devstoreaccount1`

### Container Not Found

Ensure the container exists before running backups:
```bash
az storage container create --name moco-backups --account-name <account-name>
```

## References

- [Azure Blob Storage Documentation](https://docs.microsoft.com/en-us/azure/storage/blobs/)
- [Azure SDK for Go](https://github.com/Azure/azure-sdk-for-go)
- [DefaultAzureCredential](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity#DefaultAzureCredential)
- [Workload Identity for AKS](https://docs.microsoft.com/en-us/azure/aks/workload-identity-overview)
- [Azurite - Azure Storage Emulator](https://github.com/Azure/Azurite)