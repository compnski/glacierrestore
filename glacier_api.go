package main

import (
	"io/ioutil"

	"github.com/aws/aws-sdk-go/service/glacier"
	"github.com/aws/aws-sdk-go/service/glacier/glacieriface"
)

type GlacierApi struct {
	AccountId   string
	VaultName   string
	RestoreTier string
	Service     glacieriface.GlacierAPI
}

func (api *GlacierApi) GetJobs(maxJobCount int, jobInput *glacier.ListJobsInput) (jobList []*glacier.JobDescription, err error) {
	err = api.Service.ListJobsPages(jobInput.
		SetAccountId(api.AccountId).
		SetVaultName(api.VaultName), func(resp *glacier.ListJobsOutput, last bool) bool {
		jobList = append(jobList, resp.JobList...)
		if len(jobList) > maxJobCount {
			return false
		}
		return true
	})

	return jobList, err
}

func (api *GlacierApi) SuccessfulJobs(maxJobCount int) ([]*glacier.JobDescription, error) {
	return api.GetJobs(maxJobCount, (&glacier.ListJobsInput{}).
		SetCompleted("true").
		SetStatuscode("Succeeded"))
}

func (api *GlacierApi) FailedJobs(maxJobCount int) ([]*glacier.JobDescription, error) {
	return api.GetJobs(maxJobCount, (&glacier.ListJobsInput{}).
		SetCompleted("true").
		SetStatuscode("Failed"))
}

func (api *GlacierApi) InProgressJobs(maxJobCount int) ([]*glacier.JobDescription, error) {
	return api.GetJobs(maxJobCount, (&glacier.ListJobsInput{}).
		SetCompleted("false"))
}

func (api *GlacierApi) InitiateRestoreJob(archive Archive) (string, error) {
	res, err := api.Service.InitiateJob((&glacier.InitiateJobInput{}).
		SetAccountId(api.AccountId).
		SetVaultName(api.VaultName).
		SetJobParameters((&glacier.JobParameters{}).
			SetType("archive-retrieval").
			SetArchiveId(archive.ArchiveId).
			SetDescription(archive.Path).
			SetTier(api.RestoreTier)))
	if err == nil && res != nil {
		return *res.JobId, nil
	}
	return "", err
}

func (api *GlacierApi) GetJobOutput(jobId string) (archiveDescription string, content []byte, err error) {
	res, err := api.Service.GetJobOutput((&glacier.GetJobOutputInput{}).
		SetAccountId(api.AccountId).
		SetVaultName(api.VaultName).
		SetJobId(jobId))
	if err != nil {
		return
	}
	defer res.Body.Close()
	archiveDescription = *res.ArchiveDescription
	content, err = ioutil.ReadAll(res.Body)
	return
}
