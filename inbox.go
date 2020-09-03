package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/jzelinskie/geddit"
)

// RedditMarkReadBatch is the number of comments that Reddit allows to be read at once.
const RedditMarkReadBatch = 25

// Listing is the data a listing will return.
type Listing struct {
	Data struct {
		Kind     string `json:"kind"`
		Children []struct {
			Data ListingItem `json:"data"`
		} `json:"children"`
	} `json:"data"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// ListingItem is the combined information of different listing kinds.
type ListingItem struct {
	Body string `json:"body"`
	*geddit.Submission
}

// ReplyToInbox checks the inbox and replies to mentions.
func (c *Client) ReplyToInbox() {
	processName := "Reply To Inbox"

	for !c.closed {
		c.RoutineStart(processName)
		ce := c.replyToInbox()
		if ce != nil {
			c.dfatal(ce)
		}

		c.RoutineWait(processName)
	}
}

func (c *Client) replyToInbox() *ContextError {
	// TODO: make it actually iterate backwards.

	c.Logger.Infof("Reading inbox")

	requestURL := c.makePath(RedditRouteMessageUnread + RedditJSONSuffix)
	b, ce := c.doRedditGetRequest(requestURL, url.Values{
		"limit":    []string{"100"},
		"raw_json": []string{"1"},
	})
	if ce != nil {
		return ce
	}

	listing := &Listing{}
	err := json.Unmarshal(b, listing)
	if err != nil {
		return NewContextError(err, []ContextParam{
			{"Request URL", requestURL},
			{"Request Body", string(b)},
		})
	}

	if len(listing.Data.Children) == 0 {
		c.Logger.Info("No new messages.")
	}

	var read []string
	for _, item := range listing.Data.Children {
		data := item.Data
		if strings.Contains(data.Body, "u/"+c.Config.Reddit.Username) {
			err := c.replyToComment(data)
			if err != nil {
				c.dfatal(err)
				continue
			}
			read = append(read, data.FullID)
		} else {
			c.Logger.Infof("Not a mention, skipping processing message (ID: %s).\nBody: %s", data.FullID, data.Body)
		}
	}

	return c.MarkAsRead(read)
}

// ErrCouldNotParse is the error given when a reply can't be parsed.
var ErrCouldNotParse = errors.New("Could not parse")

func (c *Client) replyToComment(l ListingItem) *ContextError {
	mention := "/u/" + c.Config.Reddit.Username + " "

	notParse := c.Config.Constants.CouldNotParse + c.Config.Constants.Footer
	if !strings.HasPrefix(l.Body, mention) {
		c.reply(l, notParse)
		return NewContextError(ErrCouldNotParse, []ContextParam{
			{"Reply Author", l.Author},
			{"Reply ID", l.FullID},
			{"Reply Link", l.Permalink},
			{"Reply Message", l.Body},
		})
	}

	body := l.Body[len(mention):]
	fields := strings.Fields(body)

	name := strings.ToLower(fields[0])

	arguments := fields[1:]

	constants := c.Config.Constants
	switch name {
	case "help":
		return c.HelpCommand(l, arguments)
	case "find":
		fallthrough
	case "search":
		return c.SearchCommand(l, arguments)
	default:
		return c.reply(l, fmt.Sprintf(constants.CouldNotParse+constants.HelpBody, fmt.Sprintf("Unknown command `%s`.", name)))
	}
}

// HelpCommand is the .
func (c *Client) HelpCommand(l ListingItem, arguments []string) *ContextError {
	constants := c.Config.Constants
	return c.reply(l, constants.HelpStart+constants.HelpBody)
}

// SearchCommand is the command to search through search terms.
func (c *Client) SearchCommand(l ListingItem, arguments []string) *ContextError {
	constants := c.Config.Constants
	couldNotParse := constants.CouldNotParse + constants.HelpBody
	if len(arguments) == 0 {
		return c.reply(l, fmt.Sprint(couldNotParse, "I need more info to find you anything."))
	}

	matches := c.getTitleMatches(arguments[0])
	var search string

	var flairArgument string
	if len(matches) == 0 {
		flairArgument = strings.Join(arguments, " ")
	} else {
		search = matches[0]
		flairArgument = strings.Join(arguments[1:], " ")
	}

	var searchResults []redis.Z
	if search != "" {
		var ce *ContextError

		searchResults, ce = c.getZSet(RedisSearchPrefix + search)
		if ce != nil {
			return ce
		}
	}

	var flairs []redis.Z
	if flairArgument != "" {
		var ce *ContextError

		flairs, ce = c.getZSet(RedisFlairsPrefix + flairArgument)
		if ce != nil {
			return ce
		}
	}

	// The round trip time to do this in a loop would be too expensive to justify. Hopefully memory doesn't overflow.
	linkMap, err := c.Redis.HGetAll(ctx, RedisLinks).Result()
	if err != nil {
		return NewContextError(err, []ContextParam{
			{"Redis Key", RedisLinks},
		})
	}

	var results []redis.Z
	if len(searchResults) != 0 && len(flairs) != 0 {
		results = c.ZIntersect(searchResults, flairs)
		if len(results) > 25 {
			results = results[:25]
		}
	} else if len(searchResults) != 0 {
		results = searchResults
	} else if len(flairs) != 0 {
		results = flairs
	}

	links := make([]string, 0, len(results))
	for _, result := range results {
		member, ok := result.Member.(string)
		if !ok {
			c.dfatal(fmt.Errorf("Expected search and title results to only contain strings, instead received member of type %T with value %s and score %f", member, member, result.Score))
			continue
		}

		links = append(links, "- "+linkMap[member])
	}

	argumentString := strings.Join(arguments, " ")
	if len(links) == 0 {
		noResults := fmt.Sprintf(constants.NoResults, argumentString)
		return c.reply(l, noResults)
	}

	allLinks := strings.Join(links, "\n\n")
	foundResults := fmt.Sprintf(constants.FoundResults+"\n\n", argumentString)
	return c.reply(l, foundResults+allLinks+constants.Footer)
}

// ZIntersect gets the intersection of two sorted sets.
func (c *Client) ZIntersect(a []redis.Z, b []redis.Z) []redis.Z {
	var larger []redis.Z
	var smaller []redis.Z
	if len(a) > len(b) {
		larger, smaller = a, b
	} else {
		larger, smaller = b, a
	}

	baseSet := make(map[redis.Z]struct{}, len(smaller))
	for _, item := range smaller {
		baseSet[item] = struct{}{}
	}

	i := 0
	keys := make([]redis.Z, len(smaller))
	for _, item := range larger {
		if _, ok := baseSet[item]; ok {
			keys[i] = item
			i++

			if i >= len(smaller) {
				break
			}
		}
	}

	return keys
}

func (c *Client) reply(l ListingItem, message string) *ContextError {
	_, ce := c.doRedditRequest("POST", c.makePath("/api/comment"), url.Values{
		"thing_id": {l.FullID},
		"text":     {message},
	}, nil)
	if ce != nil {
		return NewWrappedError("replying to comment", ce, []ContextParam{
			{"Author Reply", l.Author},
			{"Comment ID", l.FullID},
			{"Body", message},
		})
	}

	return nil
}

// RedditMarkReadPayload is the payload Reddit requires to mark submissions as read.
type RedditMarkReadPayload struct {
	ID string `json:"id"`
}

// MarkAsRead batches ids up to RedditMarkReadBatch and marks them as read.
func (c *Client) MarkAsRead(ids []string) *ContextError {
	for len(ids) > 0 {
		idBatch := ids
		if len(ids) > RedditMarkReadBatch {
			idBatch = ids[:RedditMarkReadBatch]
		}

		if len(idBatch) == 1 {
			c.Logger.Infof("Marking id: %v as read.", idBatch[0])
		} else {
			c.Logger.Infof("Marking ids: %v as read.", idBatch)
		}

		idList := strings.Join(idBatch, ",")

		// b, err := json.Marshal(RedditMarkReadPayload{idList})
		// if err != nil {
		// 	return NewContextlessError(err).Wrap("could not marshal Reddit mark read payload")
		// }

		_, ce := c.doRedditRequest("POST", c.makePath(RedditRouteReadMessage), url.Values{
			"raw_json": []string{"1"},
			"id":       []string{idList},
		}, nil)
		if ce != nil {
			return ce
		}

		ids = ids[len(idBatch):]
	}

	return nil
}
