package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type WorkflowRun struct {
	Id         int64  `json:"id"`
	Status     string `json:"status"`
	HeadSha    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	RunNumber  int    `json:"run_number"`
}

type WorkflowRunsResponse struct {
	WorkflowRuns []WorkflowRun `json:"workflow_runs"`
}

var httpClient http.Client
var githubRepo = os.Getenv("GITHUB_REPOSITORY")
var githubToken = os.Getenv("GITHUB_TOKEN")
var branchName = strings.Replace(os.Getenv("GITHUB_REF"), "refs/heads/", "", 1)
var currentSha = os.Getenv("GITHUB_SHA")
var currentRunNumber, _ = strconv.Atoi(os.Getenv("GITHUB_RUN_NUMBER"))
var wg = sync.WaitGroup{}

func githubRequest(request *http.Request) (*http.Response, error) {
	request.Header.Set("Accept", "application/vnd.github.v3+json")
	request.Header.Set("Authorization", fmt.Sprintf("token %s", githubToken))
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func cancelWorkflow(id int64) error {
	request, err := http.NewRequest("POST", fmt.Sprintf(
		"https://api.github.com/repos/%s/actions/runs/%d/cancel", githubRepo, id), nil)
	if err != nil {
		return err
	}
	response, err := githubRequest(request)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusAccepted {
		body, _ := ioutil.ReadAll(response.Body)
		return errors.New(fmt.Sprintf("failed to cancel workflow #%d, status code: %d, body: %s", id, response.StatusCode, body))
	}
	return nil
}

// I don't wan't to fail the current workflow if I fail canceling previous workflow's => so I only log errors
func main() {
	customTransport := http.DefaultTransport.(*http.Transport).Clone()
	customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	httpClient = http.Client{Transport: customTransport, Timeout: time.Minute}

	log.Printf("listing runs for branch %s in repo %s\n", branchName, githubRepo)
	request, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/actions/runs", githubRepo), nil)
	if err != nil {
		log.Println(err)
		return
	}
	query := request.URL.Query()
	query.Set("branch", branchName)
	request.URL.RawQuery = query.Encode()
	response, err := githubRequest(request)
	if err != nil {
		log.Println(err)
		return
	}
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Println(err)
		return
	}
	var workflows WorkflowRunsResponse
	if err = json.Unmarshal(body, &workflows); err != nil {
		log.Println(err)
		return
	}
	for _, run := range workflows.WorkflowRuns {
		if run.Status == "completed" {
			continue // not canceling completed jobs
		}
		if run.HeadBranch != branchName {
			continue // should not happen cuz we pre-filter, but better safe than sorry
		}
		if run.HeadSha == currentSha {
			continue // not canceling my own jobs
		}
		if currentRunNumber != 0 && run.RunNumber > currentRunNumber {
			continue // only canceling previous executions, not newer ones
		}
		log.Printf("canceling run https://github.com/%s/actions/runs/%d\n", githubRepo, run.Id)
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if err := cancelWorkflow(id); err != nil {
				log.Println(err)
			}
		}(run.Id)
	}
	wg.Wait()
}
