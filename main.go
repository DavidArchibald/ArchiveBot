package main

import (
	"github.com/jzelinskie/geddit"
)

func main() {
	client := NewClient("config.toml")
	defer client.Close()

	nextSubmissions := client.ReadPushshiftSubmissions()
	readSubmissions(client, nextSubmissions)

	go analyzeSubmissions(client)

	client.Run()
}

func readSubmissions(client *Client, nextSubmissions func() ([]PushshiftSubmission, error)) {
	submissionsCount := 0
	for {
		submissions, err := nextSubmissions()
		if err != nil {
			client.dfatal(err)
			break
		}

		if len(submissions) == 0 {
			break
		}

		if err := client.addSubmissions(submissions); err != nil {
			client.Logger.Error(err)
		}

		addedSubmissions := len(submissions)
		submissionsCount += addedSubmissions

		firstSubmission := submissions[0]
		lastSubmission := submissions[addedSubmissions-1]

		client.Logger.Infof("Added %d Pushshift submissions: %s (epoch: %f) to %s (epoch: %f).", addedSubmissions, firstSubmission.FullID, firstSubmission.DateCreated, lastSubmission.F, lastSubmission.DateCreated)
	}

	client.Logger.Infof("Added %d Pushshift submissions.", submissionsCount)
}

func analyzeSubmissions(client *Client) {
	processName := "Analyze Submissions"

	initialParams := geddit.ListingOptions{
		Limit: client.Config.Reddit.SearchLimit,
		Show:  "all",
	}

	for !client.closed {
		client.RoutineStart(processName)
		err := client.AnalyzeSubmissions(initialParams)

		if err != nil {
			client.dfatal(err)
			return
		}

		client.RoutineWait(processName)
	}
}
