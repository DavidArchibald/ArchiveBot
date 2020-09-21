package main

import (
	"time"
)

func main() {
	client := NewClient("config.toml")
	defer client.Close()

	go func() {
		for !client.closed {
			nextSubmissions := client.ReadPushshiftSubmissions()
			readSubmissions(client, nextSubmissions)
			time.After(15 * time.Minute)
		}
	}()

	go analyzeSubmissions(client)
	go client.ReplyToInbox()

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

		if err := client.Redis.addSubmissions(submissions); err != nil {
			client.dfatal(err)
			break
		}

		addedSubmissions := len(submissions)
		submissionsCount += addedSubmissions

		firstSubmission := submissions[0]
		lastSubmission := submissions[addedSubmissions-1]

		client.Logger.Infof("Added %d Pushshift submissions: %s (epoch: %f) to %s (epoch: %f).", addedSubmissions, "t3_"+firstSubmission.ID, firstSubmission.DateCreated, "t3_"+lastSubmission.ID, lastSubmission.DateCreated)
	}

	client.Logger.Infof("Added %d Pushshift submissions.", submissionsCount)
}

func analyzeSubmissions(client *Client) {
	processName := "Analyze Submissions"

	for !client.closed {
		client.Processes.RoutineStart(processName)
		err := client.AnalyzeSubmissions()

		if err != nil {
			client.dfatal(err)
		}

		client.Processes.RoutineWait(processName)
	}
}
