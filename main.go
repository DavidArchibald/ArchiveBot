package main

import (
	"fmt"
	"log"
)

func main() {
	config, err := OpenConfig("config.toml")
	if err != nil {
		log.Fatal(err)
	}

	rdb, err := NewRedisClient(config)
	if err != nil {
		log.Fatal(err)
	}

	history := NewHistory(config)
	nextSubmissions := history.ReadAllSubmissions()

	submissionsCount := 0
	for {
		submissions, err := nextSubmissions()
		if err != nil {
			log.Fatal(err)
		}

		if submissions == nil {
			break
		}

		if err := rdb.addSubmissions(submissions); err != nil {
			log.Fatal(err)
		}

		addedSubmissions := len(submissions)
		submissionsCount += addedSubmissions

		firstSubmission := submissions[0]
		lastSubmission := submissions[addedSubmissions-1]

		fmt.Printf("Added %d Submissions: %s (epoch: %d) to %s (epoch: %d).\n", addedSubmissions, firstSubmission.ID, firstSubmission.Epoch, lastSubmission.ID, lastSubmission.Epoch)
	}

	fmt.Printf("Adding %d submissions.\n", submissionsCount)
}
