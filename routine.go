package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/vartanbeno/go-reddit/reddit"
)

// AnalyzeSubmissions analyzes Reddit submissions.
// The parameters Before, After, and Count params are handled in AnalyzeSubmissions.
func (c Client) AnalyzeSubmissions(initialParams reddit.ListOptions) *ContextError {
	submissions, err := c.Redis.getHashMap(RedisSubmissions)
	if err != nil {
		if !c.IsProduction {
			return err
		}
		err.LogError(c.Logger)
	}

	submissions, err = c.analyzeRedditSubmissions(submissions, initialParams)
	if err != nil {
		if !c.IsProduction {
			return err
		}
		err.LogError(c.Logger)
	}

	ids := make([]string, len(submissions))
	i := 0
	for submission := range submissions {
		ids[i] = submission
		i++
	}

	var context []ContextParam
	if len(ids) <= 25 {
		context = []ContextParam{
			{"ids", fmt.Sprint(ids)},
		}
	}

	if len(ids) != 0 {
		err = NewContextError(errors.New("some submissions were not analyzed"), context)

		err.LogWarn(c.Logger)
	}

	return nil
}

func (c *Client) analyzeRedditSubmissions(submissionsMap map[string]string, params reddit.ListOptions) (map[string]string, *ContextError) {
	currentAnchor, ce := c.Redis.getAnchor(RedisSearchCurrent)
	if ce != nil {
		if ce.Unwrap().Error() != redis.Nil.Error() {
			ce.LogError(c.Logger)
		}
		currentAnchor = nil
	}

	if currentAnchor != nil {
		forwards, err := c.Redis.Get(ctx, RedisSearchIsForwards).Result()
		if err != nil {
			c.Logger.Errorf("Could not get search direction, default to backwards: %w", err)
			params.Before = currentAnchor.FullID
		} else {
			if forwards == "true" {
				params.After = currentAnchor.FullID
			} else {
				params.Before = currentAnchor.FullID
			}
		}
	}

	totalSubmissions := 0
	for {
		c.Reddit.Listings.GetPosts(ctx)
		r, err := c.Reddit.getSubmissions(params)
		if err != nil {
			return submissionsMap, err
		}

		submissions := r.Submissions
		submissionCount := len(submissions)
		totalSubmissions += submissionCount

		if submissionCount == 0 {
			break
		}

		for _, submission := range submissions {
			delete(submissionsMap, submission.ID)
		}

		if ce := c.Redis.updateVotes(submissions); ce != nil {
			c.dfatal(ce)
		}

		if r.Next != nil {
			params = *r.Next
		} else {
			break
		}
	}

	c.Logger.Infof("Read %d submissions.", totalSubmissions)

	return submissionsMap, nil
}

func (c *Client) checkSubmissions(posts *reddit.Posts, isForwards bool) error {
	// EDGE CASE: How to deal with <3 submissions?
	if len(posts.Posts) == 0 {
		// If you have reached the beginning or the end and are searching in that direction nothing to do.
		if (posts.After == "" && isForwards) || (posts.Before == "" && !isForwards) {
			return nil
		}
	}

	r := c.Redis
	var currentSubmission *reddit.Post
	if isForwards {
		lastSubmission := posts.Posts[len(posts.Posts)-1]
		if lastSubmission.Created != nil {
			c.Logger.Info("Time created not given for submission ID: %s (permalink: %s)", lastSubmission.FullID, lastSubmission.Permalink)
		} else if c.Search.End == nil || lastSubmission.Created.Before(c.Search.End.Epoch) {
			if ce := r.setAnchor(RedisSearchEnd, lastSubmission.FullID, *lastSubmission.Created); ce != nil {
				c.dfatal(ce)
			}
		}

		currentSubmission = lastSubmission
	} else {
		firstSubmission := posts.Posts[0]
		if firstSubmission.Created == nil {
			c.Logger.Info("Time created not given for submission ID: %s (permalink: %s)", firstSubmission.FullID, firstSubmission.Permalink)
		} else if c.Search.Start == nil || !c.Search.Start.Epoch.Before(*firstSubmission.Created) {
			if ce := r.setAnchor(RedisSearchStart, firstSubmission.FullID, *firstSubmission.Created); ce != nil {
				c.dfatal(ce)
			}
		}

		currentSubmission = firstSubmission
	}

	if currentSubmission.Created != nil {
		if ce := r.setCurrentAnchor(RedisSearchCurrent, currentSubmission.FullID, *currentSubmission.Created, isForwards); ce != nil {
			c.dfatal(ce)
		}
	}

	return nil
}

func (c *Client) makePath(route string, query url.Values) string {
	u, err := url.Parse(c.Config.Reddit.URL)
	if err != nil {
		panic(fmt.Errorf("invalid API URL: %s", c.Config.Reddit.URL))
	}

	u.Path = path.Join(u.Path, route)
	u.RawQuery = query.Encode()

	return u.String()
}

func (c *Client) makePaths(parts []string, query url.Values) string {
	return c.makePath(path.Join(parts...), query)
}

func (c *Client) doRedditRequest(req *http.Request, v interface{}) (*reddit.Response, *ContextError) {
	resp, err := c.Reddit.Do(ctx, req, v)
	if err != nil {
		return nil, NewContextlessError(err)
	}

	return resp, nil
}

func (c *Client) doRequest(method, url string, body io.Reader, header http.Header) ([]byte, *ContextError) {
	getContext := func() []ContextParam {
		var bodyString string
		var err error
		if body != nil {
			var b []byte
			b, err = ioutil.ReadAll(body)
			bodyString = string(b)
		} else {
			bodyString = fmt.Sprint(body)
		}

		baseParams := []ContextParam{
			{"Method", method},
			{"Request URL", url},
		}
		headerParam := ContextParam{"Header", fmt.Sprint(header)}

		if err == nil {
			return append(baseParams, ContextParam{"Body", bodyString}, headerParam)
		}

		return append(baseParams, ContextParam{"Body Read Error", fmt.Sprint(err)}, headerParam)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, NewContextError(err, append(
			getContext(),
			ContextParam{"requestURL", url},
		))
	}

	req.Header = header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, NewContextError(err, append(
			getContext(),
			ContextParam{"Request", fmt.Sprint(req)},
		))
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, NewContextError(err, append(
			getContext(),
			ContextParam{"Request", fmt.Sprint(req)},
			ContextParam{"Response", fmt.Sprint(resp)},
		))
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		if ce := c.doRateLimit(resp); ce != nil {
			return nil, NewContextError(ce, getContext())
		}

	} else if resp.StatusCode != http.StatusOK {
		return nil, NewContextError(errors.New("not OK status"), append(
			getContext(),
			ContextParam{"Status", fmt.Sprint(resp.StatusCode)},
		))
	}

	return b, nil
}

func (c *Client) doRateLimit(resp *http.Response) *ContextError {
	r := c.Reddit
	rateLimit, ce := r.NewRateLimit(resp)
	if ce != nil {
		return ce
	}

	r.SetRateLimit(rateLimit)
	for !c.closed && !r.IsRateLimited() {
		<-c.Processes.Tick()
	}

	return nil
}

// Anchor represents a place in the search.
type Anchor struct {
	FullID string
	Epoch  reddit.Timestamp
}

func (r *Redis) getAnchor(anchorKey string) (*Anchor, *ContextError) {
	anchorString, err := r.Get(ctx, anchorKey).Result()
	if err != nil {
		if err.Error() == redis.Nil.Error() {
			return nil, NewContextlessError(err)
		}
		return nil, NewWrappedError(fmt.Sprintf("could not get %s", anchorKey), err, nil)
	}

	anchorParts := strings.Split(anchorString, RedisDelimiter)
	if len(anchorParts) != 2 {
		return nil, NewContextError(errors.New("anchor does not have 2 parts"), []ContextParam{
			{"Anchor", anchorString},
			{"Anchor Parts", fmt.Sprint(anchorParts)},
		})
	}

	fullID, epochString := anchorParts[0], anchorParts[1]

	if err := checkFullName(fullID); err != nil {
		return nil, err.AddContext("Anchor", anchorString)
	}

	timestamp := reddit.Timestamp{}
	if timestamp.UnmarshalJSON([]byte(epochString)); err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"Anchor", anchorString},
			{"Epoch String", epochString},
		})
	}

	return &Anchor{fullID, timestamp}, nil
}

func checkFullName(fullID string) *ContextError {
	submissionPrefix := "t3_"

	// Rudimentary Reddit ID verification
	if !strings.HasPrefix(fullID, submissionPrefix) {
		return NewContextError(fmt.Errorf(`Expected fullname prefix "%s"`, submissionPrefix), []ContextParam{
			{"Input ID", fullID},
		})
	}

	baseID := fullID[len(submissionPrefix):]
	i, err := strconv.ParseInt(baseID, 36, 64)
	if err != nil {
		return NewContextError(errors.New(`Could not parse submission ID as base 36 string`), []ContextParam{
			{"Full Name Input", fullID},
			{"Submission ID", baseID},
		})
	}

	if i < 0 {
		return NewContextError(errors.New("expected parsed Reddit ID to be positive"), []ContextParam{
			{"Full Name Input", fullID},
			{"Submission ID", baseID},
			{"Submission ID Integer", fmt.Sprint(i)},
		})
	}

	if len(baseID) != 6 {
		return NewContextError(errors.New("expected Reddit ID of length 6"), []ContextParam{
			{"Full Name Input", fullID},
			{"Full Name Length", fmt.Sprint(len(fullID))},
			{"Submission ID", baseID},
		})
	}

	return nil
}
