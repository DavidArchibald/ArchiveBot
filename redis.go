package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/jzelinskie/geddit"
)

// RedisAnchorCurrent is the current anchor key.
const RedisAnchorCurrent = "currentAnchor"

// RedisAnchorStart is the start anchor key.
const RedisAnchorStart = "startAnchor"

// RedisAnchorContext is the context anchor key.
const RedisAnchorContext = "contextAnchor"

// RedisAnchorEnd is the end anchor key.
const RedisAnchorEnd = "endAnchor"

// RedisUpvotes is the key for submission upvotes.
const RedisUpvotes = "upvotes"

// RedisDelimiter is the delimiter for fields.
const RedisDelimiter = ":"

// RedisSubmissionPrefix is the prefix for a submission.
const RedisSubmissionPrefix = "submissions"

// RedisTitles is a set in the form of submissionID:title.
const RedisTitles = "titles"

// RedisPushshiftStart is the epoch of the first scanned Pushshift data.
const RedisPushshiftStart = "pushshiftStartEpoch"

// RedisPushshiftEnd is the epoch of the last scanned Pushshift data.
const RedisPushshiftEnd = "pushshiftEndEpoch"

// RedisPushshiftTraversed is a boolean value for whether to check after RedisPushshiftEnd.
const RedisPushshiftTraversed = "pushshiftTraversed"

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
	var upvotes []*redis.Z
	for _, submission := range submissions {
		keyValues = append(keyValues, RedisSubmissionPrefix+submission.ID, submission)
		titles = append(titles, submission.ID+RedisDelimiter+submission.Title)
		upvotes = append(upvotes, &redis.Z{Member: submission.ID, Score: float64(submission.Upvotes)})
	}

	r := c.Redis
	if err := r.MSet(ctx, keyValues...).Err(); err != nil {
		return fmt.Errorf("could not set submissions: %w", err)
	}

	if err := r.SAdd(ctx, RedisTitles, titles...).Err(); err != nil {
		return fmt.Errorf("could not add submission title: %w", err)
	}

	if err := r.ZAdd(ctx, RedisUpvotes, upvotes...).Err(); err != nil {
		return fmt.Errorf("could not add submission upvotes %w", err)
	}

	return nil
}

func (c *Client) getRedisSubmissions() (map[string]struct{}, *ContextError) {
	rdb := c.Redis
	submissionPrefix := RedisSubmissionPrefix + RedisDelimiter
	iter := rdb.Scan(ctx, 0, submissionPrefix+"*", 0).Iterator()

	submissions := make(map[string]struct{})
	for iter.Next(ctx) {
		id := strings.TrimLeft(iter.Val(), submissionPrefix)
		submissions[id] = struct{}{}
	}

	if err := iter.Err(); err != nil {
		return nil, NewWrappedError("error in reading Redis submissions", err, nil)
	}
	return submissions, nil
}

func (c *Client) updateVotes(submissions []*geddit.Submission) *ContextError {
	i := 0
	updates := make([]*redis.Z, len(submissions))
	for _, submission := range submissions {
		updates[i] = &redis.Z{Score: float64(submission.Ups), Member: submission.ID}
		i++
	}

	if err := c.Redis.ZAdd(ctx, RedisUpvotes, updates...).Err(); err != nil {
		return NewWrappedError("could not update submission upvotes", err, []ContextParam{
			{"updates", fmt.Sprint(updates)},
		})
	}

	return nil
}
