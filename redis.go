package main

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
)

// Redis is a client of redis information
type Redis struct {
	*redis.Client
	config RedisConfig
}

var ctx = context.Background()

// NewRedisClient creates a new Redis client.
func NewRedisClient(config RedisConfig) (*Redis, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.Addr,
		Password: config.Password,
		DB:       config.DB,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &Redis{rdb, config}, nil
}

func (c *Client) addSubmissions(submissions []PushshiftSubmission) error {
	var keyValues []interface{}
	var titles []interface{}
	var upvotes []interface{}
	for _, submission := range submissions {
		keyValues = append(keyValues, "submission:"+submission.ID, submission)
		titles = append(titles, submission.ID+":"+submission.Title)
		upvotes = append(upvotes, fmt.Sprintf("%s:%d", submission.ID, submission.Upvotes))
	}

	r := c.Redis
	if err := r.MSet(ctx, keyValues...).Err(); err != nil {
		return fmt.Errorf("could not set submissions: %w", err)
	}

	if err := r.SAdd(ctx, "titles", titles...).Err(); err != nil {
		return fmt.Errorf("could not add submission title: %w", err)
	}

	if err := r.SAdd(ctx, "upvotes", upvotes...).Err(); err != nil {
		return fmt.Errorf("could not add submission upvotes %w", err)
	}

	return nil
}
