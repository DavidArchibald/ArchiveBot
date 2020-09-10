package main

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
	"github.com/jzelinskie/geddit"
)

// Reddit Search anchors.

// RedisSearchCurrent is the current anchor key.
const RedisSearchCurrent = "searchCurrent"

// RedisSearchStart is the start anchor key.
const RedisSearchStart = "searchStart"

// RedisSearchEnd is the end anchor key.
const RedisSearchEnd = "searchEnd"

// Inbox Anchors

// RedisInboxCurrent is the currently held listing item for iteration.
const RedisInboxCurrent = "inboxCurrent"

// RedisInboxStart is the first known listing item.
const RedisInboxStart = "inboxStart"

// Pushshift Anchors

// RedisPushshiftStart is the epoch of the first scanned Pushshift data.
const RedisPushshiftStart = "pushshiftStartEpoch"

// RedisPushshiftEnd is the epoch of the last scanned Pushshift data.
const RedisPushshiftEnd = "pushshiftEndEpoch"

// RedisPushshiftTraversed is a boolean value for whether to check after RedisPushshiftEnd.
const RedisPushshiftTraversed = "pushshiftTraversed"

// RedisSearchIsForwards represents a boolean value for whether to traverse the current anchor forwards or backwards.
const RedisSearchIsForwards = "searchForwards"

// RedisDelimiter is the delimiter for fields.
const RedisDelimiter = ":"

// RedisSearchPrefix is the prefix for a search term corresponding to a set of submission IDs sorted by date created.
const RedisSearchPrefix = "search" + RedisDelimiter

// RedisUpvotes is the key for submission upvotes.
const RedisUpvotes = "upvotes"

// RedisFlairNames is a set of existing flairs
const RedisFlairNames = "flairNames"

// RedisFlairsPrefix is the prefix for a flair corresponding to a set of submission IDs sorted by date created.
const RedisFlairsPrefix = "flairs" + RedisDelimiter

// RedisSubmissions is a hash with keys of submission IDs to their JSON data.
const RedisSubmissions = "submissions"

// RedisLinks is a hash with keys of process full names to a link formatted as [title](permalink).
const RedisLinks = "links"

// RedisProcessed is a set of processed full names.
const RedisProcessed = "processed"

// Redis is a client of redis information
type Redis struct {
	*redis.Client
	client *Client
	config *Config
}

var ctx = context.Background()

// NewRedisClient creates a new Redis client.
func NewRedisClient(client *Client, config *Config) (*Redis, error) {
	redisConfig := config.Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisConfig.Addr,
		Password: redisConfig.Password,
		DB:       redisConfig.DB,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &Redis{rdb, client, config}, nil
}

func (r *Redis) addSubmissions(pushshiftSubmissions []PushshiftSubmission) error {
	var submissions []interface{}
	var links []interface{}
	var upvotes []*redis.Z

	flairs := make(map[string][]*redis.Z)
	searches := make(map[string][]*redis.Z)

	for _, submission := range pushshiftSubmissions {
		fullID := "t3_" + submission.ID
		submissions = append(submissions, fullID, submission)

		link := fmt.Sprintf("[%s](%s)", submission.Title, submission.Permalink)
		links = append(links, fullID, link)

		upvotes = append(upvotes, &redis.Z{Member: fullID, Score: float64(submission.Ups)})

		flairs[submission.LinkFlairText] = append(flairs[submission.LinkFlairText], &redis.Z{Member: fullID, Score: submission.DateCreated})

		matches := r.client.Search.getTitleMatches(submission.Title)
		for _, match := range matches {
			searches[match] = append(searches[match], &redis.Z{Score: submission.DateCreated, Member: fullID})
		}
	}

	if err := r.HSet(ctx, RedisSubmissions, submissions...).Err(); err != nil {
		return fmt.Errorf("could not set submissions: %w", err)
	}

	if err := r.HSet(ctx, RedisLinks, links...).Err(); err != nil {
		return fmt.Errorf("could not add submission title: %w", err)
	}

	if err := r.ZAdd(ctx, RedisUpvotes, upvotes...).Err(); err != nil {
		return fmt.Errorf("could not add submission upvotes: %w", err)
	}

	var flairNames []interface{}
	flairsSet := make(map[string]struct{})
	for flairName, members := range flairs {
		if err := r.ZAdd(ctx, RedisFlairsPrefix+flairName, members...).Err(); err != nil {
			return fmt.Errorf("could not add flairs for %s: %w", flairName, err)
		}

		if _, ok := flairsSet[flairName]; !ok {
			flairsSet[flairName] = struct{}{}
			flairNames = append(flairNames, flairName)
		}
	}

	if err := r.SAdd(ctx, RedisFlairNames, flairNames...).Err(); err != nil {
		return fmt.Errorf("could not add Redis flair names: %w", err)
	}

	for searchName, search := range searches {
		if err := r.ZAdd(ctx, RedisSearchPrefix+searchName, search...).Err(); err != nil {
			return fmt.Errorf("could not add search term %s: %w", searchName, err)
		}
	}

	return nil
}

func (r *Redis) getHashMap(key string) (map[string]string, *ContextError) {
	submissions, err := r.HGetAll(ctx, key).Result()

	if err != nil {
		return nil, NewWrappedError(fmt.Sprintf("error in reading %s", key), err, nil)
	}

	return submissions, nil
}

func (r *Redis) getSetMap(prefix string) (map[string][]redis.Z, *ContextError) {
	items := make(map[string][]redis.Z)

	iter := r.Scan(ctx, 0, prefix+"*", 0).Iterator()

	for iter.Next(ctx) {
		val := iter.Val()
		scores, ce := r.getZSet(val)

		if ce != nil {
			return nil, NewWrappedError(fmt.Sprintf("error in reading %s", val), ce, nil)
		}

		items[val] = scores
	}

	if err := iter.Err(); err != nil {
		return nil, NewWrappedError(fmt.Sprintf(`error in sets of prefix "%s"`, prefix), err, nil)
	}

	return items, nil
}

func (r *Redis) getZSet(name string) ([]redis.Z, *ContextError) {
	return r.getZSetRange(name, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    "+inf",
		Offset: 0,
	})
}

func (r *Redis) getZSetRange(name string, rangeBy *redis.ZRangeBy) ([]redis.Z, *ContextError) {
	result, err := r.ZRangeByScoreWithScores(ctx, name, rangeBy).Result()

	if err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"Set Key", name},
		})
	}

	return result, nil
}

func (r *Redis) updateVotes(submissions []*geddit.Submission) *ContextError {
	i := 0
	updates := make([]*redis.Z, len(submissions))
	for _, submission := range submissions {
		updates[i] = &redis.Z{Score: float64(submission.Ups), Member: submission.ID}
		i++
	}

	if err := r.ZAdd(ctx, RedisUpvotes, updates...).Err(); err != nil {
		return NewWrappedError("could not update submission upvotes", err, []ContextParam{
			{"updates", fmt.Sprint(updates)},
		})
	}

	return nil
}

func (r *Redis) setAnchor(anchorKey string, fullID string, epoch float64) *ContextError {
	anchorString := fmt.Sprintf("%s:%f", fullID, epoch)

	c := r.client
	c.Logger.Infof("Setting %s to %s.", anchorKey, anchorString)

	if err := r.Set(ctx, anchorKey, anchorString, 0).Err(); err != nil {
		c.dfatal(NewContextError(fmt.Errorf("could not set %s: %w", anchorKey, err), []ContextParam{
			{"Anchor String", anchorString},
		}))
	}

	return nil
}

func (r *Redis) setCurrentAnchor(anchorKey string, fullID string, epoch float64, isForwards bool) *ContextError {
	err := r.setAnchor(anchorKey, fullID, epoch)
	if err != nil {
		return err
	}

	if err := r.Set(ctx, RedisSearchIsForwards, isForwards, 0).Err(); err != nil {
		r.client.dfatal(NewContextlessError(fmt.Errorf("could not set %s: %w", RedisSearchIsForwards, err)))
	}

	return nil
}
