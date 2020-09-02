package main

import (
	"fmt"
	"regexp"
	"time"

	"github.com/go-redis/redis/v8"
)

// Search store information about the range of discovered information.
// The marker anchor is set to the last traversed submission and is used to resume next iteration. It is ignored on startup to update newer posts faster.
// The end anchor is the newest known locked submission. for which submissions can safely not be checked. This prevents the search from being forced to traverse every historical submission.
type Search struct {
	Current      *Anchor       // The last processed submission.
	Start        *Anchor       // The start of currently recorded submissions.
	End          *Anchor       // The newest locked submission; statistics for posts will not update past this anchor.
	HasTraversed bool          // Whether the entire history been traversed. If not, submissions after the end anchor will be analyzed.
	LockTime     time.Duration // The amount of time a submission has until it is locked. Currently 60 days.
}

// ConstantsConfig is the search data.
type ConstantsConfig struct {
	CouldNotParse string     `json:"could_not_parse"`
	HelpStart     string     `json:"help_start"`
	HelpBody      string     `json:"help_body"`
	NoResults     string     `json:"no_results"`
	Footer        string     `json:"footer"`
	Searches      [][]string `json:"searches"`
}

// NewSearch initializes all the information needed for a bidirectional search.
func NewSearch(Redis *Redis) (*Search, *ContextError) {
	anchorKeys := []string{RedisSearchCurrent, RedisSearchStart, RedisSearchEnd}
	anchors := make([]*Anchor, len(anchorKeys))

	for i, anchorKey := range anchorKeys {
		anchorString, err := Redis.Get(ctx, anchorKey).Result()
		if err != nil {
			if err.Error() == redis.Nil.Error() {
				continue
			}
			return nil, NewWrappedError(fmt.Sprintf("could not get %s", anchorKey), err, nil)
		}

		anchor, ce := getAnchor(anchorString)
		if ce != nil {
			return nil, NewWrappedError(fmt.Sprintf("could not parse %s", anchorKey), err, []ContextParam{
				{"Anchor Value", anchorString},
			})
		}
		anchors[i] = anchor
	}

	// A lock occurs after 60 days.
	lock := 60 * (time.Duration(24) * time.Hour)

	return &Search{anchors[0], anchors[1], anchors[2], false, lock, 10}, nil
}

// GetLockUnix returns the Unix time when posts will become locked.
func (s *Search) GetLockUnix() int64 {
	lockTime := time.Now().Add(-s.LockTime)
	return lockTime.Unix()
}

func (c *Client) getTitleMatches(title string) []string {
	var matches []string
	for _, searches := range c.Config.Constants.Searches {
		canonical := searches[0]
		for _, search := range searches {
			exp, err := regexp.Compile("\b" + regexp.QuoteMeta(search) + "\b")
			if err != nil {
				c.dfatal(err)
				break
			}

			if exp.MatchString(title) {
				matches = append(matches, canonical)
				break
			}
		}
	}

	return matches
}
