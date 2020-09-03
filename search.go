package main

import (
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
	TraversedAll bool          // Whether the entire history been traversed. If not, submissions after the end anchor will be analyzed.
	IsForwards   bool          // Whether the current anchor is traversing forwards or not.
	LockTime     time.Duration // The amount of time a submission has until it is locked. Currently 60 days.
	MaxRequests  int           // The max requests a search iteration can have. -1 is infinite.
}

// ConstantsConfig is the search data.
type ConstantsConfig struct {
	CouldNotParse string     `toml:"could_not_parse"`
	HelpStart     string     `toml:"help_start"`
	HelpBody      string     `toml:"help_body"`
	NoResults     string     `toml:"no_results"`
	FoundResults  string     `toml:"found_results"`
	Footer        string     `toml:"footer"`
	Searches      [][]string `toml:"searches"`
}

// NewSearch initializes all the information needed for a bidirectional search.
func NewSearch(Redis *Redis) (*Search, *ContextError) {
	anchorKeys := []string{RedisSearchCurrent, RedisSearchStart, RedisSearchEnd}
	anchors := make([]*Anchor, len(anchorKeys))

	for i, anchorKey := range anchorKeys {
		anchor, ce := Redis.getAnchor(anchorKey)
		if ce != nil && ce.Unwrap().Error() != redis.Nil.Error() {
			return nil, ce
		}
		anchors[i] = anchor
	}

	// A lock occurs after 60 days.
	lock := 60 * (time.Duration(24) * time.Hour)

	return &Search{anchors[0], anchors[1], anchors[2], false, true, lock, 10}, nil
}
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
			expr := "(?i)\\b" + regexp.QuoteMeta(search) + "\\b"
			exp, err := regexp.Compile(expr)
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
