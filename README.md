# QNAP S3 Glacier File Downloader

Downloads files from S3 Glacier and unpacks to a path specified in the ArchiveDescription metadata. The format comes from the QNAP Glacier Backup tool I used to use on my NAS.

## Usage

``` shell
Usage of glacierrestore
  -accountId string
        AWS Account ID
  -checkStatus
        If set, print a status report of all existing jobs (default true)
  -download
        If set, download content from successful jobs
  -initiateRestore
        If set, read -inventory and restore all archives
  -inventory string
        Path to inventory. Used to pull accountId/VaultName. Used to start jobs when -initateRestore is also passed.
  -maxJobCount int
        Max # of jobs that will be returned from paginated calls to the ListJobs API. There is no sorting I can find, so it's mostly useful for testing. Defaults to maxInt. (default 9223372036854775807)
  -printAllJobs
        Print downloaded Job descriptions.
  -region string
        AWS Region (default "us-east-1")
  -restorePath string
        Restore any successful jobs to this path, using the "path" from the ArchiveDescription. If set, any existing files won't re-downloaded. (default "restore/")
  -restoreTier string
        Tier for restore jobs. One of Bulk, Standard, Expedited. Check glacier docs for timing and price. Note that Expedited is VERY expensive. (default "Bulk")
-vaultName string
        S3 Glacier Vault name
```

### Required Metadata
This relies on metadata serialized to the ArchiveDescription field to store the path to restore the file.
The ArchiveDescription should be a serialized JSON object with at least a "path" key. 

This path will be appended to the restorePath to restore the archived filesystem.

### Setup
This uses your stored AWS credentials. Use `aws configure` to set that up

### Download an inventory file
Currently done via the aws cmdline tool.

``` shell
vaultName="jason-photos"
jobId=$(aws --output json glacier initiate-job \
	--account-id - \
	--vault-name "${vaultName}" \
	--job-parameters '{"Type": "inventory-retrieval"}' \
	| tee inv_job.json | jq -r .jobId)

aws --output json glacier get-job-output \
	--account-id - \
	--vault-name "${vaultName}" \
	--job-id "${jobId}" \
	inventory.json
```

Note: This will take a long time, often between 12-24 hours


### Initiate an Archive Restore from an inventory
Defaults to creating a `restore/` subfolder. Puts files based on the ArchiveDescription.
Pulls a list of jobs to not duplicate any successful or in progress jobs. If restorePath is set, any existing files will be skipped.
Stores the Path in the JobDescription for restore.
``` shell
./glacierrestore -inventory inventory.json -initiateRestore
```
Note: This will also take a long time, based on the Tier. The default, Bulk, takes about 12 hours.

### Download Archive
Defaults to creating a `restore/` subfolder. Puts files on the filesytem based on the JobDescription field, which is set from the ArchiveDescription.
The inventory file is optional, but will provide the vaultName and accountId, based on the VaultARN inside.

``` shell
./glacierrestore -inventory inventory.json -download 
```
I seem to download 27MB files in ~3 seconds, maybe 10MB/s.
