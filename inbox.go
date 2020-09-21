package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/vartanbeno/go-reddit/reddit"
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
	*reddit.Post
}

// ReplyToInbox checks the inbox and replies to mentions.
func (c *Client) ReplyToInbox() {
	processName := "Reply To Inbox"

	p := c.Processes
	for !c.closed {
		p.RoutineStart(processName)
		ce := c.replyToInbox()
		if ce != nil {
			c.dfatal(ce)
		}

		p.RoutineWait(processName)
	}
}

func (c *Client) replyToInbox() *ContextError {
	// TODO: make it actually iterate backwards.

	c.Logger.Infof("Reading inbox")

	comments, _, _, err := c.Reddit.Message.InboxUnread(ctx, &reddit.ListOptions{
		Limit: 100,
	})
	if err != nil {
		return NewContextlessError(err)
	}

	if len(comments.Messages) == 0 {
		c.Logger.Info("No new comments.")
	}

	var read []string
	for _, item := range comments.Messages {
		if strings.Contains(item.Text, "u/"+c.Config.Reddit.Username) {
			err := c.replyToComment(item)
			if err != nil {
				c.dfatal(err)
				continue
			}
			read = append(read, item.FullID)
		} else {
			c.Logger.Infof("Not a mention, skipping processing message (ID: %s).\nBody: %s", item.FullID, item.Text)
		}
	}

	return c.MarkAsRead(read)
}

// ErrCouldNotParse is the error given when a reply can't be parsed.
var ErrCouldNotParse = errors.New("Could not parse")

func (c *Client) replyToComment(l *reddit.Message) *ContextError {
	parts := strings.SplitN(l.Text, "u/"+c.Config.Reddit.Username, 2)
	if len(parts) == 1 {
		notParse := c.Config.Constants.CouldNotParse + c.Config.Constants.Footer
		c.reply(l, notParse)
		c.Logger.DPanic("Message has no mention in it!")
		return NewContextError(ErrCouldNotParse, []ContextParam{
			{"Reply Author", l.Author},
			{"Reply ID", l.FullID},
			{"Reply Text", l.Text},
		})
	}

	mentionLine := parts[1]
	fields := strings.Fields(mentionLine)

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
func (c *Client) HelpCommand(m *reddit.Message, arguments []string) *ContextError {
	constants := c.Config.Constants
	return c.reply(m, constants.HelpStart+constants.HelpBody)
}

// SearchCommand is the command to search through search terms.
func (c *Client) SearchCommand(m *reddit.Message, arguments []string) *ContextError {
	constants := c.Config.Constants
	couldNotParse := constants.CouldNotParse + constants.HelpBody
	if len(arguments) == 0 {
		return c.reply(m, fmt.Sprint(couldNotParse, "I need more info to find you anything."))
	}

	matches := c.Search.getTitleMatches(arguments[0])
	var search string

	var flairArgument string
	if len(matches) == 0 {
		flairArgument = strings.Join(arguments, " ")
	} else {
		search = matches[0]
		flairArgument = strings.Join(arguments[1:], " ")
	}

	r := c.Redis
	var searchResults []redis.Z
	if search != "" {
		var ce *ContextError

		searchResults, ce = r.getZSet(RedisSearchPrefix + search)
		if ce != nil {
			return ce
		}
	}

	var flairs []redis.Z
	if flairArgument != "" {
		var ce *ContextError

		flairs, ce = r.getZSet(RedisFlairsPrefix + flairArgument)
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

	// The round trip time to do this in a loop would be too expensive to justify. Hopefully memory doesn't overflow.
	submissions, err := c.Redis.HGetAll(ctx, RedisSubmissions).Result()
	if err != nil {
		return NewContextError(err, []ContextParam{
			{"Redis Key", RedisLinks},
		})
	}

	var allResults []redis.Z
	if len(searchResults) != 0 && len(flairs) != 0 {
		allResults = c.ZIntersect(searchResults, flairs)
	} else if len(searchResults) != 0 {
		allResults = searchResults
	} else if len(flairs) != 0 {
		allResults = flairs
	}

	results := make([]redis.Z, 0, 25)
	i := 0
	for _, result := range allResults {
		if i >= 25 {
			break
		}

		if _, ok := submissions[result.Member.(string)]; ok {
			results[i] = result
			i++
		}
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
		return c.reply(m, noResults)
	}

	allLinks := strings.Join(links, "\n\n")
	foundResults := fmt.Sprintf(constants.FoundResults+"\n\n", argumentString)
	return c.reply(m, foundResults+allLinks+constants.Footer)
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
			if i >= len(smaller) {
				break
			}

			keys[i] = item
			i++
		}
	}

	return keys
}

func (c *Client) reply(m *reddit.Message, message string) *ContextError {
	_, _, err := c.Reddit.Comment.Submit(ctx, m.FullID, message)

	if err != nil {
		return NewWrappedError("replying to comment", err, []ContextParam{
			{"Author Reply", m.Author},
			{"Comment ID", m.FullID},
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

		_, err := c.Reddit.Message.Read(ctx, idBatch...)
		if err != nil {
			return NewWrappedError("setting message read", err, []ContextParam{
				{"ids", fmt.Sprint(idBatch)},
			})
		}

		processed := make([]interface{}, len(idBatch))
		for i, id := range idBatch {
			processed[i] = id
		}

		if err := c.Redis.SAdd(ctx, RedisProcessed, processed...).Err(); err != nil {
			return NewWrappedError("could not add processed submissions to Redis", err, []ContextParam{
				{"IDs", fmt.Sprint(idBatch)},
			})
		}

		ids = ids[len(idBatch):]
	}

	return nil
}
