package main

import (
	"github.com/jzelinskie/geddit"
)

// RedditConfig is the toml configuration for Reddit.
type RedditConfig struct {
	Username    string `toml:"username"`
	Password    string `toml:"password"`
	UserAgent   string `toml:"user_agent"`
	SearchLimit int    `toml:"search_limit"`
	URL         string `toml:"url"`
}

// Reddit is the structure for Reddit.
type Reddit struct {
	*geddit.LoginSession
	config RedditConfig
}

// NewRedditClient creates a new Reddit client.
func NewRedditClient(config RedditConfig) (*Reddit, error) {
	session, err := geddit.NewLoginSession(config.Username, config.Password, config.UserAgent)
	if err != nil {
		return nil, err
	}

	return &Reddit{session, config}, nil
}
