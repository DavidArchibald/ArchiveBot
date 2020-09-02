package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/jzelinskie/geddit"
)

// RedisSearchCurrent is the current anchor key.
const RedisSearchCurrent = "searchCurrent"

// RedisSearchStart is the start anchor key.
const RedisSearchStart = "searchStart"

// RedisSearchEnd is the end anchor key.
const RedisSearchEnd = "searchEnd"

// RedisInboxCurrent is the currently held listing item for iteration.
const RedisInboxCurrent = "inboxCurrent"

// RedisInboxStart is the first known listing item.
const RedisInboxStart = "inboxStart"

// RedisUpvotes is the key for submission upvotes.
const RedisUpvotes = "upvotes"

// RedisDelimiter is the delimiter for fields.
const RedisDelimiter = ":"

// RedisSubmissionPrefix is the prefix for a submission.
const RedisSubmissionPrefix = "submissions"

// RedisTitles is a hash with keys of submissionID to title.
const RedisTitles = "titles"

// RedisPushshiftStart is the epoch of the first scanned Pushshift data.
const RedisPushshiftStart = "pushshiftStartEpoch"

// RedisPushshiftEnd is the epoch of the last scanned Pushshift data.
const RedisPushshiftEnd = "pushshiftEndEpoch"

// RedisPushshiftTraversed is a boolean value for whether to check after RedisPushshiftEnd.
const RedisPushshiftTraversed = "pushshiftTraversed"

// RedisSearchIsForwards represents a boolean value for whether to traverse the current anchor forwards or backwards.
const RedisSearchIsForwards = "searchForwards"

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
	var submissionMaps []interface{}
	var titles []interface{}
	var upvotes []*redis.Z
	for _, submission := range submissions {
		submissionMaps = append(submissionMaps, RedisSubmissionPrefix+submission.FullID, submission)
		titles = append(titles, submission.FullID, submission.Title)
		upvotes = append(upvotes, &redis.Z{Member: submission.FullID, Score: float64(submission.Ups)})
	}

	r := c.Redis
	if err := r.MSet(ctx, submissionMaps...).Err(); err != nil {
		return fmt.Errorf("could not set submissions: %w", err)
	}

	if err := r.HSet(ctx, RedisTitles, titles...).Err(); err != nil {
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

func (c *Client) setAnchor(anchorKey string, fullID string, epoch float64) *ContextError {
	anchorString := fmt.Sprintf("%s:%f", fullID, epoch)

	c.Logger.Infof("Setting %s to %s.", anchorKey, anchorString)

	if err := c.Redis.Set(ctx, anchorKey, anchorString, 0).Err(); err != nil {
		c.dfatal(NewContextError(fmt.Errorf("could not set %s: %w", anchorKey, err), []ContextParam{
			{"Anchor String", anchorString},
		}))
	}

	return nil
}

func (c *Client) setCurrentAnchor(anchorKey string, fullID string, epoch float64, isForwards bool) *ContextError {
	err := c.setAnchor(anchorKey, fullID, epoch)
	if err != nil {
		return err
	}

	if err := c.Redis.Set(ctx, RedisSearchIsForwards, isForwards, 0).Err(); err != nil {
		c.dfatal(NewContextlessError(fmt.Errorf("could not set %s: %w", RedisSearchIsForwards, err)))
	}

	return nil
}
