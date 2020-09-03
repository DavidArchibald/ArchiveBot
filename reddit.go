package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jzelinskie/geddit"
)

// RedditConfig is the toml configuration for Reddit.
type RedditConfig struct {
	Username     string `toml:"username"`
	Password     string `toml:"password"`
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	RedirectURI  string `toml:"redirect_uri"`
	UserAgent    string `toml:"user_agent"`
	SearchLimit  int    `toml:"search_limit"`
	URL          string `toml:"url"`
}

// Reddit is the structure for Reddit.
type Reddit struct {
	sessionCookie *http.Cookie
	modhash       string
	config        RedditConfig
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

// RedditSubredditPrefix is the prefix of a subreddit's name.
const RedditSubredditPrefix = "/r/"

// RedditJSONSuffix makes sure Reddit replies using JSON.
const RedditJSONSuffix = ".json"

// RedditRouteMessageUnread is for unread messages in your inbox.
const RedditRouteMessageUnread = "/message/unread"

// RedditRouteReadMessage is for marking messages in your inbox as read.
const RedditRouteReadMessage = "/api/read_message"

// RedditRouteComments is the route for getting comments.
const RedditRouteComments = "/comments"

// NewRedditClient creates a new Reddit client.
func NewRedditClient(config RedditConfig) (*Reddit, error) {
	// A copy and paste from geddit.NewLoginSession to expose its values.

	username := config.Username
	password := config.Password

	loginURL := fmt.Sprintf("https://www.reddit.com/api/login/%s", username)
	postValues := url.Values{
		"user":     {username},
		"passwd":   {password},
		"api_type": {"json"},
	}

	req, err := http.NewRequest("POST", loginURL, strings.NewReader(postValues.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", config.UserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}

	var sessionCookie *http.Cookie
	// Get the session cookie.
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "reddit_session" {
			sessionCookie = cookie
		}
	}

	// Get the modhash from the JSON.
	type Response struct {
		JSON struct {
			Errors [][]string
			Data   struct {
				Modhash string
			}
		}
	}

	r := &Response{}
	err = json.NewDecoder(resp.Body).Decode(r)
	if err != nil {
		return nil, err
	}

	if len(r.JSON.Errors) != 0 {
		var msg []string
		for _, k := range r.JSON.Errors {
			msg = append(msg, k[1])
		}
		return nil, errors.New(strings.Join(msg, ", "))
	}

	return &Reddit{sessionCookie, r.JSON.Data.Modhash, config}, nil
}

// func authenticateInBrowser() {
// 	url := authenticator.GetAuthenticationURL()
// 	err := browser.OpenURL(url)
// 	if err != nil {
// 		fmt.Printf("Could not open browser: %s\nPlease open URL manually: %s\n", err, url)
// 	}

// 	http.HandleFunc("/")
// 	http.ListenAndServe()
// }

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
	baseURL := c.makePath(RedditSubredditPrefix, c.Config.Subreddit.Name)
	route := fmt.Sprintf("%s/%s.json", baseURL, geddit.NewSubmissions)

	resp, ce := c.doRedditGetRequest(route, url.Values{
		"t":        []string{params.Time},
		"limit":    []string{fmt.Sprint(params.Limit)},
		"after":    []string{params.After},
		"before":   []string{params.After},
		"count":    []string{fmt.Sprint(params.Count)},
		"show":     []string{params.Show},
		"article":  []string{params.Article},
		"raw_json": []string{"1"},
	})
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
	if err != nil {
		return nil, NewContextlessError(err).Wrap("could not check submissions")
	}

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
