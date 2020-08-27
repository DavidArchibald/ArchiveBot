package main

import (
	"context"

	"github.com/go-redis/redis/v8"
)

// Redis is a client of redis information
type Redis struct {
	*redis.Client
	Config *Config
}

var ctx = context.Background()

// NewRedisClient creates a new Redis client.
func NewRedisClient(config *Config) (*Redis, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.Redis.Addr,
		Password: config.Redis.Password,
		DB:       config.Redis.DB,
	})

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}

	return &Redis{rdb, config}, nil
}

func (r *Redis) addSubmissions(submissions []PushshiftSubmission) error {
	var arguments []interface{}
	for _, submission := range submissions {
		arguments = append(arguments, "submission:"+submission.ID, submission)
	}
	_, err := r.MSet(ctx, arguments...).Result()
	return err
}
