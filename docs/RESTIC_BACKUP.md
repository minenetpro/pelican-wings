# Restic Backup Integration

This document describes the restic-based backup adapter for Pelican Wings, which stores server snapshots in an S3-compatible repository.

## Overview

The restic backup adapter provides an alternative to the traditional local and S3 backup methods. Instead of creating tar.gz archives, it uses [restic](https://restic.net/) to create deduplicated, encrypted snapshots stored in an S3 repository.

**Key Features:**
- Deduplicated backups (only changed data is stored)
- Encryption at rest
- Multi-tenant support with strict isolation
- External backup management (Panel/external service controls when backups happen)
- Automatic repository initialization

## Configuration

Add the following to your Wings configuration file (`/etc/pelican/config.yml`):

```yaml
system:
  backups:
    write_limit: 0
    compression_level: "best_speed"
    restic:
      enabled: true
      binary_path: "/usr/bin/restic"
      cache_dir: "/var/cache/pelican/restic"
```

Repository selection and credentials are request-scoped. The control plane must send a `restic_config` payload with each restic operation.

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the restic backup adapter |
| `binary_path` | string | `restic` | Path to the restic binary |
| `cache_dir` | string | `/var/cache/pelican/restic` | Directory for restic cache files |

## Prerequisites

1. **Restic Binary**: Install restic on the Wings host
   ```bash
   # Debian/Ubuntu
   apt install restic

   # RHEL/CentOS
   yum install restic

   # Or download from GitHub releases
   wget https://github.com/restic/restic/releases/download/v0.16.0/restic_0.16.0_linux_amd64.bz2
   bunzip2 restic_0.16.0_linux_amd64.bz2
   chmod +x restic_0.16.0_linux_amd64
   mv restic_0.16.0_linux_amd64 /usr/bin/restic
   ```

2. **S3 Bucket**: Create an S3 bucket with appropriate permissions
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Action": [
           "s3:GetObject",
           "s3:PutObject",
           "s3:DeleteObject",
           "s3:ListBucket"
         ],
         "Resource": [
           "arn:aws:s3:::my-backup-bucket",
           "arn:aws:s3:::my-backup-bucket/*"
         ]
       }
     ]
   }
   ```

## API Endpoints

### Create Backup

Creates a new restic snapshot of the server's data directory.

```http
POST /api/servers/{server}/backup
Authorization: Bearer {token}
Content-Type: application/json

{
    "adapter": "restic",
    "uuid": "backup-uuid-from-external-service",
    "ignore": "",
    "restic_config": {
        "repository_key": "team-storage-config-id",
        "repository": "s3:https://ACCOUNT.r2.cloudflarestorage.com/team-bucket",
        "password": "restic-password",
        "aws_access_key_id": "temporary-access-key",
        "aws_secret_access_key": "temporary-secret",
        "aws_session_token": "temporary-session-token",
        "aws_region": "auto"
    }
}
```

**Response:** `202 Accepted`

The backup runs asynchronously. The snapshot is tagged with:
- `backup_uuid:{uuid}` - Identifies this specific backup
- `server_uuid:{server}` - Identifies the server

### Restore Backup

Restores a server from a restic snapshot. **Cross-server restore is supported** - you can restore a backup from one server to a different server.

```http
POST /api/servers/{server}/backup/{backup}/restore
Authorization: Bearer {token}
Content-Type: application/json

{
    "adapter": "restic",
    "truncate_directory": true,
    "restic_config": {
        "repository_key": "team-storage-config-id",
        "repository": "s3:https://ACCOUNT.r2.cloudflarestorage.com/team-bucket",
        "password": "restic-password",
        "aws_access_key_id": "temporary-access-key",
        "aws_secret_access_key": "temporary-secret",
        "aws_session_token": "temporary-session-token",
        "aws_region": "auto"
    }
}
```

**Response:** `202 Accepted`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `adapter` | string | Yes | Must be `"restic"` |
| `truncate_directory` | bool | No | If true, deletes all server files before restoring |

**Cross-Server Restore:**
- The `{server}` in the URL is the **target** server where files will be restored
- The `{backup}` UUID identifies the backup snapshot (which may have been created from a different server)
- Files from the original server's directory are extracted directly into the target server's directory

### Delete Backup

Removes a snapshot from the restic repository. The server UUID in the URL is
not validated for restic snapshots; the backup UUID tag identifies the snapshot.

```http
DELETE /api/servers/{server}/backup/{backup}
Authorization: Bearer {token}
Content-Type: application/json

{
    "adapter": "restic",
    "restic_config": {
        "repository_key": "team-storage-config-id",
        "repository": "s3:https://ACCOUNT.r2.cloudflarestorage.com/team-bucket",
        "password": "restic-password",
        "aws_access_key_id": "temporary-access-key",
        "aws_secret_access_key": "temporary-secret",
        "aws_session_token": "temporary-session-token",
        "aws_region": "auto"
    }
}
```

**Response:** `204 No Content`

This runs `restic forget --tag backup_uuid:{backup} --prune` to remove the snapshot and clean up unreferenced data.

### List Snapshots

Lists all restic snapshots for a server. The server UUID is used only as a tag
filter and does not need to exist on the node.

```http
POST /api/servers/{server}/backup/snapshots
Authorization: Bearer {token}
Content-Type: application/json
```

```json
{
    "include_stats": false,
    "restic_config": {
        "repository_key": "team-storage-config-id",
        "repository": "s3:https://ACCOUNT.r2.cloudflarestorage.com/team-bucket",
        "password": "restic-password",
        "aws_access_key_id": "temporary-access-key",
        "aws_secret_access_key": "temporary-secret",
        "aws_session_token": "temporary-session-token",
        "aws_region": "auto"
    }
}
```

| Body Field | Type | Default | Description |
|------------|------|---------|-------------|
| `include_stats` | bool | `false` | Include size and file count (slower, requires additional restic command per snapshot) |
| `restic_config` | object | - | Request-scoped repository configuration |

**Response:** `200 OK`

```json
{
    "snapshots": [
        {
            "id": "d95a2254abcd1234efgh5678ijkl9012mnop3456",
            "short_id": "d95a2254",
            "time": "2024-02-02T17:32:46.258Z",
            "backup_uuid": "a09316f2-c1df-44b9-8244-6c6789eb75r1",
            "server_uuid": "342dd230-48d3-4b39-a7fe-ba0fb5e62e80",
            "paths": ["/var/lib/pelican/volumes/342dd230-48d3-4b39-a7fe-ba0fb5e62e80"]
        }
    ]
}
```

**Response with `include_stats=true`:** `200 OK`

```json
{
    "snapshots": [
        {
            "id": "d95a2254abcd1234efgh5678ijkl9012mnop3456",
            "short_id": "d95a2254",
            "time": "2024-02-02T17:32:46.258Z",
            "backup_uuid": "a09316f2-c1df-44b9-8244-6c6789eb75r1",
            "server_uuid": "342dd230-48d3-4b39-a7fe-ba0fb5e62e80",
            "paths": ["/var/lib/pelican/volumes/342dd230-48d3-4b39-a7fe-ba0fb5e62e80"],
            "size": 1073741824,
            "total_file_count": 5432
        }
    ]
}
```

### Get Snapshot Status

Checks if a specific backup snapshot exists in the restic repository. The
server UUID in the URL is not validated for restic snapshots; the backup UUID tag identifies the snapshot.

```http
POST /api/servers/{server}/backup/{backup}/status
Authorization: Bearer {token}
Content-Type: application/json
```

```json
{
    "include_stats": false,
    "restic_config": {
        "repository_key": "team-storage-config-id",
        "repository": "s3:https://ACCOUNT.r2.cloudflarestorage.com/team-bucket",
        "password": "restic-password",
        "aws_access_key_id": "temporary-access-key",
        "aws_secret_access_key": "temporary-secret",
        "aws_session_token": "temporary-session-token",
        "aws_region": "auto"
    }
}
```

**Response (snapshot exists):** `200 OK`

```json
{
    "exists": true,
    "snapshot": {
        "id": "d95a2254abcd1234efgh5678ijkl9012mnop3456",
        "short_id": "d95a2254",
        "time": "2024-02-02T17:32:46.258Z",
        "backup_uuid": "a09316f2-c1df-44b9-8244-6c6789eb75r1",
        "server_uuid": "342dd230-48d3-4b39-a7fe-ba0fb5e62e80",
        "paths": ["/var/lib/pelican/volumes/342dd230-48d3-4b39-a7fe-ba0fb5e62e80"],
        "size": 1073741824,
        "total_file_count": 5432
    }
}
```

**Response (snapshot not found):** `200 OK`

```json
{
    "exists": false,
    "snapshot": null
}
```

## SSE Events

Subscribe to backup events via Server-Sent Events (SSE) at:

```http
GET /api/events?servers={server-uuid}
Authorization: Bearer {token}
```

The SSE endpoint streams real-time events including backup completion and restore completion events.

### backup completed

Sent when a backup operation completes (success or failure).

```json
{
    "server_id": "342dd230-48d3-4b39-a7fe-ba0fb5e62e80",
    "backup_uuid": "a09316f2-c1df-44b9-8244-6c6789eb75r1",
    "is_successful": true,
    "checksum": "",
    "checksum_type": "sha1",
    "file_size": 0
}
```

| Field | Description |
|-------|-------------|
| `server_id` | UUID of the server |
| `backup_uuid` | UUID of the backup |
| `is_successful` | Whether the backup completed successfully |
| `checksum` | Empty for restic backups (restic handles checksums internally) |
| `checksum_type` | Always "sha1" |
| `file_size` | 0 for restic backups (size not tracked locally) |

### backup restore completed

Sent when a backup restore operation completes.

```json
{
    "server_id": "342dd230-48d3-4b39-a7fe-ba0fb5e62e80",
    "backup_uuid": "a09316f2-c1df-44b9-8244-6c6789eb75r1"
}
```

| Field | Description |
|-------|-------------|
| `server_id` | UUID of the server |
| `backup_uuid` | UUID of the backup that was restored |

## Multi-Tenant Security

The restic adapter implements strict isolation between servers/customers:

1. **Tagging**: Every snapshot is tagged with both `backup_uuid:{uuid}` and `server_uuid:{uuid}`

2. **Host Isolation**: Each backup sets `--host {server_uuid}` to identify the source

3. **Path Isolation**: Each server's data is backed up from its unique path:
   ```
   /var/lib/pelican/volumes/{server_uuid}/
   ```

4. **Strict Filtering**: All operations (restore, delete, status check) filter by `backup_uuid` tag, preventing access to other snapshots

5. **Server-Scoped Listing**: The snapshot listing endpoint only returns snapshots for the specified server (filtered by `server_uuid` tag), even if the server no longer exists on the node

## How It Works

### Backup Flow

1. External service calls Wings API with `backup_uuid` and server UUID
2. Wings ensures the restic repository exists (auto-initializes if needed)
3. Wings executes: `restic backup --host {server_uuid} --tag backup_uuid:{uuid} --tag server_uuid:{uuid} /var/lib/pelican/volumes/{server_uuid}`
4. Wings notifies the Panel of backup completion

### Restore Flow

1. External service calls Wings API with backup UUID and target server UUID
2. Wings finds the snapshot by querying: `restic snapshots --json --tag backup_uuid:{uuid}`
3. Wings extracts the original server's path from the snapshot metadata
4. Wings executes: `restic restore {snapshot_id}:{original_path} --target {target_server_path}`
5. Files are restored directly from the original server's directory into the target server's directory
6. Wings notifies the Panel of restore completion

**Note:** This approach supports cross-server restore - the backup from server A can be restored to server B. The `snapshotID:path` syntax ensures files are placed directly in the target directory without nested paths.

### Delete Flow

1. External service calls Wings API with backup UUID
2. Wings executes: `restic forget --tag backup_uuid:{uuid} --prune`
3. The snapshot and any unreferenced data are removed

## Repository Auto-Initialization

On the first backup, Wings automatically initializes the restic repository if it doesn't exist:

1. Wings attempts to list snapshots to check if the repo exists
2. If the repo doesn't exist, Wings runs `restic init`
3. This is a one-time operation protected by a per-repository mutex to prevent concurrent init attempts

## Environment Variables

The following environment variables are set when executing restic commands:

| Variable | Source |
|----------|--------|
| `RESTIC_REPOSITORY` | `request.restic_config.repository` |
| `RESTIC_PASSWORD` | `request.restic_config.password` |
| `AWS_ACCESS_KEY_ID` | `request.restic_config.aws_access_key_id` |
| `AWS_SECRET_ACCESS_KEY` | `request.restic_config.aws_secret_access_key` |
| `AWS_SESSION_TOKEN` | `request.restic_config.aws_session_token` |
| `AWS_DEFAULT_REGION` | `request.restic_config.aws_region` |

## Differences from Traditional Backups

| Feature | Local/S3 Backup | Restic Backup |
|---------|-----------------|---------------|
| Format | tar.gz archive | Restic snapshots |
| Deduplication | No | Yes |
| Encryption | No (unless S3 SSE) | Yes (always) |
| Checksum | SHA1 of archive | Internal to restic |
| Size tracking | Yes | No (managed by restic) |
| Per-file restore events | Yes | No |
| Storage location | Local disk or S3 presigned URL | Restic S3 repository |

## Troubleshooting

### Common Issues

**Repository not found errors:**
- Verify the caller is sending `restic_config`
- Verify S3 credentials are correct
- Check bucket exists and is accessible
- Ensure AWS region is set correctly

**Permission denied:**
- Check restic binary has execute permissions
- Verify Wings user can read server data directories
- Check S3 IAM permissions

**Backup fails with "unable to open config file":**
- Repository may not be initialized
- Wings will auto-initialize, but check for credential issues

### Debug Logging

Enable debug mode in Wings to see detailed restic command output:

```yaml
debug: true
```

This will log:
- Full restic commands being executed
- stdout/stderr from restic operations
- Snapshot lookup results

## Verification Commands

Manually verify backups using the restic CLI:

```bash
# Set environment variables
export RESTIC_REPOSITORY="s3:s3.us-east-1.amazonaws.com/my-backup-bucket"
export RESTIC_PASSWORD="secure-restic-repository-password"
export AWS_ACCESS_KEY_ID="AKIAIOSFODNN7EXAMPLE"
export AWS_SECRET_ACCESS_KEY="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

# List all snapshots
restic snapshots

# List snapshots for a specific backup
restic snapshots --tag backup_uuid:abc-123-def

# List snapshots for a specific server
restic snapshots --tag server_uuid:server-456

# Check repository integrity
restic check
```
