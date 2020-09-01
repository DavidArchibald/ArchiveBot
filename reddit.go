package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/go-querystring/query"
	"github.com/jzelinskie/geddit"
)

// RedditConfig is the toml configuration for Reddit.
type RedditConfig struct {
	Username    string `toml:"username"`
	Password    string `toml:"password"`
	UserAgent   string `toml:"user_agent"`
	SearchLimit int    `toml:"search_limit"`
	URL         string `toml:"url"`
}

// Reddit is the structure for Reddit.
type Reddit struct {
	*geddit.LoginSession
	config RedditConfig
}

// SubmissionData is the data Reddit's submission listing returns.
// A blank before, after, and zero children means either the post is deleted or the only one in the subreddit.
type SubmissionData struct {
	Data struct {
		Children []struct {
			Data *geddit.Submission
		}
		Before string `json:"before"`
		After  string `json:"after"`
	}
}

// SubmissionsResponse is the response of a submission listing.
type SubmissionsResponse struct {
	Submissions []*geddit.Submission
	Next        *geddit.ListingOptions
}

// NewRedditClient creates a new Reddit client.
func NewRedditClient(config RedditConfig) (*Reddit, error) {
	session, err := geddit.NewLoginSession(config.Username, config.Password, config.UserAgent)
	if err != nil {
		return nil, err
	}

	return &Reddit{session, config}, nil
}

func (c *Client) getSubmissions(params geddit.ListingOptions) (*SubmissionsResponse, *ContextError) {
	isForwards := true // Synonymous with using after
	searchDescriptor := ""
	if params.Before != "" && params.After != "" {
		err := NewContextError(errors.New("both before and after param is set"), []ContextParam{
			{"params", fmt.Sprint(params)},
		})
		c.dfatal(err)
		return nil, err
	} else if params.Before != "" {
		isForwards = false
		searchDescriptor = "before " + params.Before
	} else if params.After != "" {
		searchDescriptor = "after " + params.After
	} else {
		searchDescriptor = "from start"
	}

	c.Logger.Infof("Reading submissions %s.", searchDescriptor)
	v, err := query.Values(params)
	if err != nil {
		return nil, NewWrappedError("could not create query parameters", err, []ContextParam{
			{"params", fmt.Sprint(params)},
		})
	}
	baseURL := c.makePath("/r/", c.Config.Subreddit.Name)
	url := fmt.Sprintf("%s/%s.json?%s", baseURL, geddit.NewSubmissions, v.Encode())

	resp, ce := c.redditGetRequest(url)
	if ce != nil {
		return nil, ce
	}

	r := new(SubmissionData)
	if err := json.Unmarshal(resp, r); err != nil {
		return nil, NewWrappedError("could not read response", err, []ContextParam{
			{"resp", fmt.Sprint(resp)},
		})
	}

	submissions, err := c.checkSubmissions(r, isForwards)

	next := &geddit.ListingOptions{}
	*next = params
	next.Count += len(submissions)
	if params.Before != "" {
		next.Before = r.Data.Before
		if next.Before == "" {
			next = nil
		}
	} else {
		next.After = r.Data.After
		if next.After == "" {
			next = nil
		}
	}

	return &SubmissionsResponse{submissions, next}, nil
}
