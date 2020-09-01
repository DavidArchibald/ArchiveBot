package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jzelinskie/geddit"
)

// AnalyzeSubmissions analyzes Reddit submissions.
// The parameters Before, After, and Count params are handled in AnalyzeSubmissions.
func (c Client) AnalyzeSubmissions(initialParams geddit.ListingOptions) *ContextError {
	submissions, err := c.getRedisSubmissions()
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

func (c *Client) analyzeRedditSubmissions(submissionsSet map[string]struct{}, params geddit.ListingOptions) (map[string]struct{}, *ContextError) {
	for {
		r, err := c.getSubmissions(params)
		if err != nil {
			return submissionsSet, err
		}

		submissions := r.Submissions
		submissionCount := len(submissions)

		if submissionCount == 0 {
			break
		}

		for _, submission := range submissions {
			delete(submissionsSet, submission.ID)
		}

		if ce := c.updateVotes(submissions); ce != nil {
			c.dfatal(ce)
		}

		if r.Next != nil {
			params = *r.Next
		} else {
			break
		}
	}

	return submissionsSet, nil
}

func (c *Client) checkSubmissions(r *SubmissionData, isForwards bool) ([]*geddit.Submission, error) {
	submissions := make([]*geddit.Submission, len(r.Data.Children))
	for i, child := range r.Data.Children {
		submissions[i] = child.Data
	}

	// EDGE CASE: How to deal with <3 submissions?
	if len(submissions) == 0 {
		// If you have reached the beginning or the end and are searching in that direction nothing to do.
		if (r.Data.After == "" && isForwards) || (r.Data.Before == "" && !isForwards) {
			return submissions, nil
		}
	}

	var anchors []interface{}
	if isForwards {
		lastSubmission := submissions[len(submissions)-1]
		endAnchor := lastSubmission.FullID + RedisDelimiter + fmt.Sprintf("%f", lastSubmission.DateCreated)

		anchors = []interface{}{
			RedisAnchorEnd, endAnchor,
		}

		c.Logger.Infof("Setting End Anchor to %s.", endAnchor)
	} else {
		startAnchor := fmt.Sprintf("%s:%f", submissions[0].FullID, submissions[0].DateCreated)
		anchors = []interface{}{
			RedisAnchorStart, startAnchor,
		}

		c.Logger.Infof("Setting Start Anchor to %s.", startAnchor)
	}

	lastSubmission := submissions[len(submissions)-1]
	currentAnchor := fmt.Sprintf("%s:%f", lastSubmission.FullID, lastSubmission.DateCreated)
	anchors = append(anchors, RedisAnchorCurrent, currentAnchor)
	c.Logger.Infof("Setting Current Anchor to %s.",  currentAnchor)

	if err := c.Redis.MSet(ctx, anchors...).Err(); err != nil {
		c.dfatal(NewContextError(fmt.Errorf("could not set anchor(s): %w", err), []ContextParam{
			{"Anchor Update", fmt.Sprint(anchors)},
		}))
	}

	return submissions, nil
}

func (c *Client) getSubmission(id string) (*geddit.Submission, *ContextError) {
	c.Logger.Infof("Processing submission %s", id)
	apiURL := c.makePath("/comments", id+".json")

	b, err := c.redditGetRequest(apiURL)
	if err != nil {
		return nil, NewWrappedError("could not get submission", err, []ContextParam{
			{"requestURL", apiURL},
		})
	}

	var comments []json.RawMessage
	if err := json.Unmarshal(b, &comments); err != nil {
		return nil, NewWrappedError("request unmarshalling", err, []ContextParam{
			{"requestURL", apiURL},
			{"responseData", fmt.Sprintf(`"%s"`, b)},
		})
	}

	if len(comments) == 0 {
		return nil, NewContextError(errors.New("no comments in data"), []ContextParam{
			{"requestURL", apiURL},
			{"responseData", fmt.Sprintf(`"%s"`, b)},
		})
	}

	submission := &geddit.Submission{}
	if err := json.Unmarshal(comments[0], submission); err != nil {
		return nil, NewContextError(errors.New("can't parse submission"), []ContextParam{
			{"requestURL", apiURL},
			{"commentData", fmt.Sprintf(`"%s"`, comments[0])},
		})
	}

	return submission, nil
}

func (c *Client) makePath(parts ...string) string {
	u, err := url.Parse(c.Config.Reddit.URL)
	if err != nil {
		panic(fmt.Errorf("invalid API URL: %s", c.Config.Reddit.URL))
	}

	parts = append(parts, u.Path)
	u.Path = path.Join(parts...)

	return u.String()
}

func (c *Client) redditGetRequest(url string) ([]byte, *ContextError) {
	return c.doRequest("GET", url, nil, http.Header{
		"User-Agent": []string{c.Config.Reddit.UserAgent},
	})
}

func (c *Client) doRequest(method, url string, body io.Reader, header http.Header) ([]byte, *ContextError) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"requestURL", url},
		})
	}

	req.Header = header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"request", fmt.Sprint(req)},
		})
	}
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"request", fmt.Sprint(req)},
			{"response", fmt.Sprint(resp)},
		})
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			remainingHeader := resp.Header.Get("X-Ratelimit-Remaining")
			resetHeader := resp.Header.Get("X-Ratelimit-Reset")

			remaining, err := strconv.Atoi(remainingHeader)
			if err != nil {
				return nil, NewWrappedError("could not parse rate limit header", err, []ContextParam{
					{"X-Ratelimit-Remaining", remainingHeader},
					{"All Headers", fmt.Sprint(resp.Header)},
				})
			}

			reset, err := strconv.Atoi(resetHeader)
			if err != nil {
				return nil, NewWrappedError("could not parse reset limit header", err, []ContextParam{
					{"X-Ratelimit-Reset", resetHeader},
					{"All Headers", fmt.Sprint(resp.Header)},
				})
			}

			if remaining < 1 {
				time.Sleep(time.Duration(reset))
			} else {
				time.Sleep(time.Duration(1))
			}

			return c.doRequest(method, url, body, header)
		}

		return nil, NewContextError(errors.New("not OK status"), []ContextParam{
			{"status", fmt.Sprint(resp.StatusCode)},
			{"responseBody", string(b)},
		})
	}

	return b, nil
}

// Anchor represents a place in the search.
type Anchor struct {
	FullID string
	Epoch  float64
}

func getAnchor(anchor string) (*Anchor, *ContextError) {
	anchorParts := strings.Split(anchor, RedisDelimiter)
	if len(anchorParts) != 2 {
		return nil, NewContextError(errors.New("anchor does not have 2 parts"), []ContextParam{
			{"Anchor", anchor},
			{"Anchor Parts", fmt.Sprint(anchorParts)},
		})
	}

	fullID, epochString := anchorParts[0], anchorParts[1]

	if err := checkFullName(fullID); err != nil {
		return nil, err.AddContext("Anchor", anchor)
	}

	var epoch float64
	var err error
	if epoch, err = epochToFloat64(epochString); err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"Anchor", anchor},
			{"Epoch String", epochString},
		})
	}

	return &Anchor{fullID, epoch}, nil
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

func epochToFloat64(epoch string) (float64, error) {
	i, err := strconv.ParseFloat(epoch, 64)
	if err != nil {
		return 0, err
	}
	return i, nil
}
