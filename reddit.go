package main

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/vartanbeno/go-reddit/reddit"
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
	*reddit.Client
	http      *http.Client
	client    *Client
	lock      sync.Locker
	rateLimit *Limiter
}

// Limiter limits how often a can occur.
type Limiter struct {
	currentRateLimit *RateLimit
	rateLimitDone    chan struct{}
}

// RateLimit is a rate limit returned by Reddit.
type RateLimit struct {
	time      time.Time
	used      int
	remaining int
	reset     int
	resetTime time.Time
}

func (r *RateLimit) String() string {
	return fmt.Sprintf("used: %d, remaining: %d, reset: %d", r.used, r.remaining, r.reset)
}

// SubmissionData is the data Reddit's post listing returns.
// A blank before, after, and zero children means either the post is deleted or the only one in the subreddit.
type SubmissionData struct {
	Data struct {
		Children []struct {
			Data *reddit.Post
		}
		Before string `json:"before"`
		After  string `json:"after"`
	}
}

// SubmissionsResponse is the response of a submission listing.
type SubmissionsResponse struct {
	Submissions []*reddit.Post
	Next        *reddit.ListOptions
}

// RedditSubredditPrefix is the prefix of a subreddit's name.
const RedditSubredditPrefix = "/r/"

// NewRedditClient creates a new Reddit client.
func NewRedditClient(client *Client, config *Config) (*Reddit, error) {
	h := &http.Client{}

	r, err := reddit.NewClient(h, &reddit.Credentials{
		ID:       config.Reddit.ClientID,
		Secret:   config.Reddit.ClientSecret,
		Username: config.Reddit.Username,
		Password: config.Reddit.Password,
	})

	if err != nil {
		return nil, err
	}

	ticker := make(chan struct{}, 1)
	limiter := &Limiter{nil, ticker}

	return &Reddit{r, h, client, &sync.Mutex{}, limiter}, nil
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

// func (r *Reddit) getSubmissions(params reddit.ListOptions) (*SubmissionsResponse, *ContextError) {
// 	c := r.client

// 	isForwards := true // Synonymous with using after
// 	searchDescriptor := ""
// 	if params.Before != "" && params.After != "" {
// 		err := NewContextError(errors.New("both before and after param is set"), []ContextParam{
// 			{"params", fmt.Sprint(params)},
// 		})
// 		c.dfatal(err)
// 		return nil, err
// 	} else if params.Before != "" {
// 		isForwards = false
// 		searchDescriptor = "before " + params.Before
// 	} else if params.After != "" {
// 		searchDescriptor = "after " + params.After
// 	} else {
// 		searchDescriptor = "from start"
// 	}

// 	c.Logger.Infof("Reading submissions %s.", searchDescriptor)
// 	posts, _, err := r.Subreddit.NewPosts(ctx, c.Config.Subreddit.Name, &reddit.ListOptions{
// 		Limit:  c.Config.Reddit.SearchLimit,
// 		After:  params.After,
// 		Before: params.Before,
// 	})

// 	err = c.checkSubmissions(posts, isForwards)
// 	if err != nil {
// 		return nil, NewContextlessError(err).Wrap("could not check submissions")
// 	}

// 	next := &reddit.ListOptions{}
// 	*next = params
// 	if params.Before != "" {
// 		next.Before = posts.Before
// 		if next.Before == "" {
// 			next = nil
// 		}
// 	} else {
// 		next.After = posts.After
// 		if next.After == "" {
// 			next = nil
// 		}
// 	}

// 	return &SubmissionsResponse{posts.Posts, next}, nil
// }

// NewRateLimit constructs a rate limit from a response.
func (r *Reddit) NewRateLimit(resp *http.Response) (*RateLimit, *ContextError) {
	headers := [...]string{"X-Ratelimit-Used", "X-Ratelimit-Remaining", "X-Ratelimit-Reset"}
	headerInts := [...]int{0, 0, 0}

	for i, header := range headers {
		headerString := resp.Header.Get(header)
		headerInt, err := strconv.Atoi(headerString)
		if err != nil {
			return nil, NewWrappedError("could not parse rate limit header", err, []ContextParam{
				{header, headerString},
				{"All Headers", fmt.Sprint(resp.Header)},
			})
		}

		headerInts[i] = headerInt
	}

	now := time.Now()
	used, remaining, reset := headerInts[0], headerInts[1], headerInts[2]
	resetTime := now.Add(time.Duration(reset) * time.Second)

	return &RateLimit{now, used, remaining, reset, resetTime}, nil
}

// SetRateLimit sets the rate limit if it is newer than the currently set one.
func (r *Reddit) SetRateLimit(newLimit *RateLimit) {
	c := r.client

	c.Reddit.lock.Lock()
	defer c.Reddit.lock.Unlock()

	currentLimit := c.Reddit.RateLimit()
	if currentLimit == nil {
		c.Logger.Infof("First rate limit: %s", newLimit)
		c.Reddit.rateLimit.currentRateLimit = newLimit
		return
	}

	if currentLimit.time.Before(newLimit.time) {
		c.Reddit.rateLimit.currentRateLimit = newLimit
	} else {
		return
	}

	if newLimit.remaining == 0 {
		c.Logger.Infof("Used all limits approximately %d seconds until reset.", currentLimit.reset)
	}

	if currentLimit.remaining == 0 && newLimit.remaining != 0 {
		givenDuration := time.Duration(currentLimit.reset)
		realDuration := time.Now().Sub(currentLimit.time)
		difference := givenDuration - realDuration

		if difference > -1 && difference < 1 {
			c.Logger.Infof("Limits reset after %v.", realDuration)
		} else {
			c.Logger.Infof("Limits reset after %v (expected %v).", realDuration, givenDuration)
		}
	}
}

// RateLimit gets a copy of the rate limit.
func (r *Reddit) RateLimit() *RateLimit {
	if r.rateLimit == nil || r.rateLimit.currentRateLimit == nil {
		return nil
	}

	currentLimit := &RateLimit{}
	*currentLimit = *r.rateLimit.currentRateLimit

	return currentLimit
}

// IsRateLimited checks to see if the process is rate limited.
func (r *Reddit) IsRateLimited() bool {
	if r.rateLimit == nil || r.rateLimit.currentRateLimit != nil {
		return false
	}

	return time.Now().Before(r.rateLimit.currentRateLimit.resetTime)
}
