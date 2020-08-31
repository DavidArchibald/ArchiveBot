package main

import (
	"log"

	"go.uber.org/zap"
)

// Client is the encapsulation of the relevant clients used.
type Client struct {
	*Flags
	Logger    *zap.SugaredLogger
	Config    *Config
	Redis     *Redis
	Reddit    *Reddit
	History   *History
	waitClose chan struct{}
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

	waitClose := make(chan struct{}, 1)

	return &Client{flags, logger, config, rdb, reddit, history, waitClose}
}

// Close the client's functions.
func (c *Client) Close() {
	c.Redis.Close()
	c.Logger.Sync()
	c.waitClose <- struct{}{}
}

// WaitClose is used to wait until the client closes.
func (c *Client) WaitClose() {
	<-c.waitClose
	close(c.waitClose)
}
