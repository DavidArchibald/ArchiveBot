package main

import (
	"log"

	"go.uber.org/zap"
)

// Client is the encapsulation of the relevant clients used.
type Client struct {
	*Flags
	Logger  *zap.SugaredLogger
	Config  *Config
	Redis   *Redis
	Reddit  *Reddit
	History *History
}

// Flags are the application flags, sourced from Config.Application, hoisted for convenience.
type Flags struct {
	IsProduction bool
}

// NewClient creates the needed Client connections.
func NewClient(configPath string) *Client {
	config, err := OpenConfig(configPath)
	if err != nil {
		log.Fatalf("could not open config: %v", err)
	}

	flags := &Flags{config.Application.IsProduction}
	logger, err := NewLogger(flags.IsProduction)
	if err != nil {
		log.Fatal("could not creator logger: %w", err)
	}

	rdb, err := NewRedisClient(config.Redis)
	if err != nil {
		logger.Panicf("could not create Redis client: %v", err)
	}

	reddit, err := NewRedditClient(config.Reddit)
	if err != nil {
		defer rdb.Close()
		logger.Panicf("could not create Reddit client: %v", err)
	}

	history := NewHistory(config)

	return &Client{flags, logger, config, rdb, reddit, history}
}

// Close the client's functions.
func (c *Client) Close() {
	c.Redis.Close()
	c.Logger.Sync()
}

func main() {
	client := NewClient("config.toml")
	defer client.Close()

	nextSubmissions := client.ReadAllSubmissions()
	readSubmissions(client, nextSubmissions)

	if err := client.AnalyzeSubmissions(); err != nil {
		client.fatal(err)
	}
}

func readSubmissions(client *Client, nextSubmissions func() ([]PushshiftSubmission, error)) {
	submissionsCount := 0
	for {
		submissions, err := nextSubmissions()
		if err != nil {
			client.dfatal(err)
			continue
		}

		if submissions == nil {
			break
		}

		if err := client.addSubmissions(submissions); err != nil {
			client.Logger.Error(err)
		}

		addedSubmissions := len(submissions)
		submissionsCount += addedSubmissions

		firstSubmission := submissions[0]
		lastSubmission := submissions[addedSubmissions-1]

		client.Logger.Infof("Added %d Pushshift submissions: %s (epoch: %d) to %s (epoch: %d).", addedSubmissions, firstSubmission.ID, firstSubmission.Epoch, lastSubmission.ID, lastSubmission.Epoch)
	}

	client.Logger.Infof("Added %d Pushshift submissions.", submissionsCount)
}
