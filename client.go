package main

import (
	"log"

	"go.uber.org/zap"
)

// Client is the encapsulation of the relevant clients used.
type Client struct {
	*Flags
	Logger          *zap.SugaredLogger
	Config          *Config
	Redis           *Redis
	Reddit          *Reddit
	Search          *Search
	PushshiftSearch *PushshiftSearch
	Processes       *Processes
	closed          bool // Can only be set to true, once.
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

	search, ce := NewSearch(rdb)
	if ce != nil {
		ce.Panic(logger)
	}

	pushshiftSearch := NewPushshiftSearch(config)

	processes := NewProcesses()

	return &Client{flags, logger, config, rdb, reddit, search, pushshiftSearch, processes, false}
}

// Close the client's functions.
func (c *Client) Close() {
	c.Redis.Close()
	c.Logger.Sync()
	c.CloseProcesses()
}

// Run is used to control the event loop until the client closes.
func (c *Client) Run() {
	c.StartProcesses()
}
