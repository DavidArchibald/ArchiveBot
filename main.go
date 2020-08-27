package main

import (
	"encoding/json"
	"log"
)

func main() {
	config, err := OpenConfig("config.toml")
	if err != nil {
		log.Fatal(err)
	}

	history := NewHistory(config)
	nextSubmissions := history.ReadAllSubmissions()

	for {
		submissions, err := nextSubmissions()
		if err != nil {
			log.Fatal(err)
		}
		json.Marshal(submissions)
	}
}
