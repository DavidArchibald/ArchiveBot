package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/pelletier/go-toml"
)

// Config is the toml config that houses all of ArchiveBot's information.
type Config struct {
	Subreddit Subreddit
	Pushshift Pushshift
	Redis     Redis
}

// Subreddit is the configuration for a subreddit.
type Subreddit struct {
	Name  string
	Limit int64
}

// Pushshift is the configuration for Pushshift.
type Pushshift struct {
	URL   string
	Delay int64
}

// Redis configuration
type Redis struct {
	Addr     string
	Password string
	DB       int64
}

// OpenConfig opens the configuration file.
func OpenConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("Cannot open config: %w", err)
	}

	byt, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("Could not read config: %w", err)
	}

	config := &Config{}
	if err := toml.Unmarshal(byt, config); err != nil {
		return nil, fmt.Errorf("Could not parse toml: %w", err)
	}

	return config, nil
}
