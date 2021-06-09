package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/glacier"
)

// TODO
// Download inventory
// Compare SHA256 checksum?
// Size printouts / confirmation
// Uploads
// progress / status

var printAllJobs = flag.Bool("printAllJobs", false, "Print downloaded Job descriptions.")

func main() {
	var maxJobCount = flag.Int("maxJobCount", math.MaxInt64, "Max # of jobs that will be returned from paginated calls to the ListJobs API. There is no sorting I can find, so it's mostly useful for testing. Defaults to maxInt.")
	var inventoryPath = flag.String("inventory", "", "Path to inventory. Used to pull accountId/VaultName. Used to start jobs when -initateRestore is also passed.")
	var initiateRestore = flag.Bool("initiateRestore", false, "If set, read -inventory and restore all archives")
	var restoreBasePath = flag.String("restorePath", "restore/", "Restore any successful jobs to this path, using the \"path\" from the ArchiveDescription. If set, any existing files won't re-downloaded.")
	var checkStatus = flag.Bool("checkStatus", true, "If set, print a status report of all existing jobs")
	var downloadJobOutput = flag.Bool("download", false, "If set, download content from successful jobs")
	var vaultName = flag.String("vaultName", "", "S3 Glacier Vault name")
	var accountId = flag.String("accountId", "", "AWS Account ID")
	var awsRegion = flag.String("region", "us-east-1", "AWS Region")
	var restoreTier = flag.String("restoreTier", "Bulk", "Tier for restore jobs. One of Bulk, Standard, Expedited. Check glacier docs for timing and price. Note that Expedited is VERY expensive.")

	flag.Parse()

	var (
		inventory       *Inventory
		err             error
		existingJobsMap = map[string]*glacier.JobDescription{}
		successJobs     []*glacier.JobDescription
		shouldFetchJobs = *checkStatus || *downloadJobOutput || *initiateRestore
	)

	if *inventoryPath != "" {
		inventory, err = ReadInventoryFile(*inventoryPath)
		if err != nil {
			panic(err)
		}
		// Read accountId and vaultName from inventory file
		vaultArn, err := arn.Parse(inventory.VaultARN)
		if err != nil {
			panic(err)
		}
		if *accountId == "" {
			accountId = &vaultArn.AccountID
		} else if *accountId != vaultArn.AccountID {
			log.Fatalf("AccountId doesn't match inventory file: AccountID=%s vaultARN=%s", *accountId, vaultArn)
		}
		parsedVaultName := filepath.Base(vaultArn.Resource)
		if *vaultName == "" {
			vaultName = &parsedVaultName
		} else if *vaultName != parsedVaultName {
			log.Fatalf("VaultName doesn't match inventory file: VaultName=%s vaultARN=%s", *vaultName, vaultArn)
		}
	}

	if *accountId == "" || *vaultName == "" {
		flag.PrintDefaults()
		log.Fatalf("AccountId and VaultName blank. Either pass in an inventory with -inventory or specify with -accountId and -vaultName")
	}

	gapi := &GlacierApi{
		Service:     glacier.New(session.Must(session.NewSession()), &aws.Config{Region: awsRegion}),
		AccountId:   *accountId,
		VaultName:   *vaultName,
		RestoreTier: *restoreTier,
	}

	if shouldFetchJobs {
		successJobs, existingJobsMap, err = FetchJobs(gapi, *maxJobCount)
		if err != nil {
			panic(err)
		}
	}

	if *initiateRestore && inventory != nil && len(inventory.ArchiveList) > 0 {
		RestoreFromInventory(gapi, inventory, *restoreBasePath, existingJobsMap)
	}

	if *downloadJobOutput {
		for _, job := range successJobs {
			if *job.Action != "ArchiveRetrieval" {
				continue
			}
			err = RestoreDataFromCompletedJob(gapi, *restoreBasePath, job)
			if err != nil {
				panic(err)
			}
		}
	}

	if err != nil {
		panic(err)
	}
}

type JobFetcher interface {
	SuccessfulJobs(maxJobCount int) ([]*glacier.JobDescription, error)
	FailedJobs(maxJobCount int) ([]*glacier.JobDescription, error)
	InProgressJobs(maxJobCount int) ([]*glacier.JobDescription, error)
}

func FetchJobs(gapi JobFetcher, maxJobCount int) (successJobs []*glacier.JobDescription, existingJobsMap map[string]*glacier.JobDescription, err error) {
	existingJobsMap = map[string]*glacier.JobDescription{}
	failedJobs, err := gapi.FailedJobs(maxJobCount)
	if err != nil {
		log.Printf("Failed to retrieve failed jobs: %v", fmtAWSErr(err))
	}

	successJobs, err = gapi.SuccessfulJobs(maxJobCount)
	if err != nil {
		log.Printf("Failed to retrieve success jobs: %v", fmtAWSErr(err))
		return nil, nil, err
	}

	inProgressJobs, err := gapi.InProgressJobs(maxJobCount)
	if err != nil {
		log.Printf("Failed to retrieve in progress jobs: %v", fmtAWSErr(err))
		return nil, nil, err
	}
	log.Printf("%d Failed, %d Succeeded %d InProgress", len(failedJobs), len(successJobs), len(inProgressJobs))

	if *printAllJobs {
		log.Printf("Failed:\n%+v\n\nSucceeded:\n%+v\n\nInProgress:\n%+v", failedJobs, successJobs, inProgressJobs)
	}

	for _, job := range append(inProgressJobs, successJobs...) {
		if *job.Action != "ArchiveRetrieval" {
			continue
		}
		if existingJob, exists := existingJobsMap[*job.ArchiveId]; exists {
			log.Printf("Duplicate job for id %s, Previous job: %+v", *job.ArchiveId, *existingJob.JobId)
		}
		existingJobsMap[*job.ArchiveId] = job
	}
	return
}

type RestoreInitiator interface {
	InitiateRestoreJob(Archive) (jobId string, err error)
}

func RestoreFromInventory(api RestoreInitiator, inventory *Inventory, restoreBasePath string, existingJobsMap map[string]*glacier.JobDescription) {
	for _, archive := range inventory.ArchiveList {
		if existingJob, exists := existingJobsMap[archive.ArchiveId]; exists {
			log.Printf("Existing %s job for archive %s, created at %s", *existingJob.StatusCode, archive.ArchiveId, *existingJob.CreationDate)
			continue
		}
		filePath, err := RestoredFilePath(restoreBasePath, archive.Path)
		if err != nil {
			panic(err)
		}
		// Check for existing file, don't run if restored file will exist
		if _, err := os.Stat(filePath); !errors.Is(err, os.ErrNotExist) {
			log.Printf("Skipping existing file at %s", filePath)
			continue
		}

		jobId, err := api.InitiateRestoreJob(archive)
		if err != nil {
			panic(err)
		}
		log.Printf("Created job id %s for archive %s", jobId, archive.ArchiveId)
	}
}

func RestoredFilePath(restoreBasePath, archivePath string) (string, error) {
	return filepath.Abs(filepath.Join(restoreBasePath, archivePath))
}

type JobOutputer interface {
	GetJobOutput(jobId string) (archiveDescription string, archiveBody []byte, err error)
}

func RestoreDataFromCompletedJob(api JobOutputer, restoreBasePath string, job *glacier.JobDescription) error {
	if job.JobDescription == nil {
		log.Printf("Skipping job with empty description: %+v", job)
		return nil
	}
	restoreFilePath, err := RestoredFilePath(restoreBasePath, *job.JobDescription)
	if _, err = os.Stat(restoreFilePath); !errors.Is(err, os.ErrNotExist) {
		log.Printf("Skipping existing file at %s", restoreFilePath)
		return nil
	}
	_, content, err := api.GetJobOutput(*job.JobId)
	if err == nil {
		if restoreFilePath != "" {
			log.Printf("Restoring %d bytes of data into file %s", *job.ArchiveSizeInBytes, restoreFilePath)
			err = WriteDataFile(restoreFilePath, content)
		}
	}
	return err
}

func WriteDataFile(realPath string, content []byte) error {
	err := os.MkdirAll(filepath.Dir(realPath), 0755)
	if err == nil {
		err = ioutil.WriteFile(realPath, content, 0644)
	}
	return err
}

func fmtAWSErr(err error) string {
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case glacier.ErrCodeResourceNotFoundException:
				return fmt.Sprintln(glacier.ErrCodeResourceNotFoundException, aerr.Error())
			case glacier.ErrCodeInvalidParameterValueException:
				return fmt.Sprintln(glacier.ErrCodeInvalidParameterValueException, aerr.Error())
			case glacier.ErrCodeMissingParameterValueException:
				return fmt.Sprintln(glacier.ErrCodeMissingParameterValueException, aerr.Error())
			case glacier.ErrCodeServiceUnavailableException:
				return fmt.Sprintln(glacier.ErrCodeServiceUnavailableException, aerr.Error())
			default:
				return fmt.Sprintln(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			return fmt.Sprintln(err.Error())
		}
	}
	return ""
}
