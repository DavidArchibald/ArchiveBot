package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/pelletier/go-toml"
)

// Config is the toml config that houses all of ArchiveBot's information.
type Config struct {
	Application Application  `toml:"Application"`
	Subreddit   Subreddit    `toml:"Subreddit"`
	Pushshift   Pushshift    `toml:"Pushshift"`
	Redis       RedisConfig  `toml:"Redis"`
	Reddit      RedditConfig `toml:"Reddit"`
}

// Application is
type Application struct {
	IsProduction bool          `toml:"is_production"`
	LoopDelay    time.Duration `toml:"loop_delay"`
	TickSpeed    time.Duration `toml:"tick_speed"`
}

// Subreddit is the configuration for a subreddit.
type Subreddit struct {
	Name  string `toml:"name"`
	Limit int64  `toml:"search_limit"`
}

// Pushshift is the configuration for Pushshift.
type Pushshift struct {
	URL   string `toml:"url"`
	Delay int64  `toml:"delay"`
}

// RedisConfig configuration
type RedisConfig struct {
	Addr     string `toml:"addr"`
	Password string `toml:"password"`
	DB       int    `toml:"db"`
}

// OpenConfig opens the configuration file.
func OpenConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("cannot open config: %w", err)
	}
	defer file.Close()

	byt, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("could not read config: %w", err)
	}

	config := &Config{}
	if err := toml.Unmarshal(byt, config); err != nil {
		return nil, fmt.Errorf("could not parse toml: %w", err)
	}

	return config, nil
}
