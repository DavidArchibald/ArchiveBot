package main

import (
	"log"
	"sync"
	"time"

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
	ticker          chan struct{}
	wg              *sync.WaitGroup
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

	ticker := make(chan struct{})
	wg := &sync.WaitGroup{}

	return &Client{flags, logger, config, rdb, reddit, search, pushshiftSearch, ticker, wg, false}
}

// Close the client's functions.
func (c *Client) Close() {
	c.Redis.Close()
	c.Logger.Sync()
	c.closed = true
	<-c.ticker
	close(c.ticker)
}

// Run is used to control the event loop until the client closes.
func (c *Client) Run() {
	timer := time.NewTicker(c.Config.Application.LoopDelay)
	go func() {
		for !c.closed {
			c.ticker <- struct{}{}
			c.wg.Wait()
			timer.Reset(c.Config.Application.LoopDelay)
			<-timer.C
		}
	}()

	checker := time.NewTicker(time.Second)
	for !c.closed {
		<-checker.C
	}

	timer.Reset(0)
	checker.Stop()
	close(c.ticker)
}
