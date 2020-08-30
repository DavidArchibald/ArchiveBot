package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jzelinskie/geddit"
)

// AnalyzeSubmissions analyzes Reddit submissions.
func (c Client) AnalyzeSubmissions() *ContextError {
	rdb := c.Redis
	submissionPrefix := "submission:"
	iter := rdb.Scan(ctx, 0, submissionPrefix+"*", 0).Iterator()

	var keyValues []interface{}
	for iter.Next(ctx) {
		id := strings.TrimLeft(iter.Val(), submissionPrefix)
		submission, err := c.getSubmission(id)
		if err != nil {
			if status := err.GetContext("status"); status != "" {
				NewWrappedError("skipping submission", err.Unwrap(), nil).LogWarn(c.Logger)
				continue
			}
			return err.UnwrapContext().Wrap("error analyzing submission")
		}
		keyValues = append(keyValues, "votes:"+id, submission.Ups)
	}

	if err := iter.Err(); err != nil {
		return NewWrappedError("error in reading Redis iteration", err, nil)
	}

	return nil
}

// GetVote records a submissions' votes.
func (c *Client) GetVote(id string) *ContextError {
	submission, err := c.getSubmission(id)
	if err != nil {
		return err
	}

	if err := c.Redis.ZAdd(ctx, "votes", &redis.Z{Score: float64(submission.Ups), Member: id}).Err(); err != nil {
		return NewWrappedError("could not set votes", err, []ContextParam{
			{"id", id},
		})
	}

	return nil
}

func (c *Client) getSubmission(id string) (*geddit.Submission, *ContextError) {
	c.Logger.Infof("Processing submission %s", id)
	apiURL := c.makePath("/comments", id+".json")

	b, err := c.doRequest(apiURL)
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

func (c *Client) doRequest(url string) ([]byte, *ContextError) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"requestURL", url},
		})
	}

	req.Header.Set("User-Agent", c.Config.Reddit.UserAgent)

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

			c.doRequest(url)
		}

		return nil, NewContextError(errors.New("not OK status"), []ContextParam{
			{"status", fmt.Sprint(resp.StatusCode)},
			{"responseBody", string(b)},
		})
	}

	return b, nil
}
