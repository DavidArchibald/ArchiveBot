package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// PushshiftSearch is a structure to traverse history through Pushshift.
type PushshiftSearch struct {
	last *PushshiftSubmission
	done bool
}

// NewPushshiftSearch constructs a search .
func NewPushshiftSearch(config *Config) *PushshiftSearch {
	return &PushshiftSearch{}
}

// PushshiftData is the structure of the returned pushshift data
type PushshiftData struct {
	Data []PushshiftSubmission `json:"data"`
}

// PushshiftSubmission is the extracted information from a Pushshift message.
type PushshiftSubmission struct {
	PushshiftFields
	Raw map[string]interface{}
}

// PushshiftFields are the extracted fields of a submission.
// geddit.Submission is not necessarily guaranteed to match and so is not used.
type PushshiftFields struct {
	Permalink     string  `json:"permalink"`
	FullID        string  `json:"name"`
	Title         string  `json:"title"`
	Ups           int     `json:"ups"`
	DateCreated   float64 `json:"created_utc"`
	LinkFlairText string  `json:"link_flair_text"`
}

// UnmarshalJSON from bytes.
func (s *PushshiftSubmission) UnmarshalJSON(b []byte) error {
	var fields PushshiftFields
	if err := json.Unmarshal(b, &fields); err != nil {
		return NewContextError(err, []ContextParam{
			{"responseData", fmt.Sprintf(`"%s"`, b)},
		})
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return NewContextError(err, []ContextParam{
			{"responseData", fmt.Sprintf(`"%s"`, b)},
		})
	}

	*s = PushshiftSubmission{fields, raw}
	return nil
}

// MarshalJSON turns into a submission into JSON.
func (s *PushshiftSubmission) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Raw)
}

// MarshalBinary turns the JSON string into raw bytes.
func (s PushshiftSubmission) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(s.Raw); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ErrSubmissionsRead is returned with ReadSubmissions has finished reading all submissions.
var ErrSubmissionsRead = errors.New("all submissions read")

// ErrMaySkipSubmissions is returned when submissions may be skipped.
// This occurs when a request returns submissions starting and ending with the same epoch.
// Because paging submissions is dependent on the epoch, if a page is filled with the same epoch .
// For example, if the limit is 10 and there are 10 posts with the same epoch going to the next epoch
var ErrMaySkipSubmissions = errors.New("submissions may be skipped")

// ErrInvalidLimit occurs when the limit is invalid.
var ErrInvalidLimit = errors.New("invalid limit")

// ReadPushshiftSubmissions gets every submission with an appropriate delay between.
func (c *Client) ReadPushshiftSubmissions() func() ([]PushshiftSubmission, error) {
	return func() ([]PushshiftSubmission, error) {
		history := c.PushshiftSearch
		config := c.Config
		submissions, err := c.ReadSubmissionBatch()
		if err != nil && !errors.Is(err, ErrSubmissionsRead) {
			return nil, err
		}

		if history.done && submissions == nil {
			return nil, nil
		}

		time.Sleep(time.Duration(config.Pushshift.Delay))

		return submissions, nil
	}
}

// ReadSubmissionBatch reads a batch of submissions from Pushshift.
// ReadSubmissionBatch will panic if given a limit less than 2. There is no known downside to putting the limit to the current maximum of 500, the minimum suggested is 10.
// The length returned will be less than or equal to the limit, usually about the limit - 1 after the first request. This is to prevent submissions being skipped.
// If the error is the sentinel error ErrSubmissionsRead, any submissions are still valid. Otherwise if a submission is returned with an error, it is unintended behavior.
func (c *Client) ReadSubmissionBatch() ([]PushshiftSubmission, error) {
	history := c.PushshiftSearch
	config := c.Config
	if config.Subreddit.Limit <= 2 {
		c.Logger.DPanic(
			fmt.Errorf("expected limit to be at least 2, %w", ErrInvalidLimit),
		)
	}

	if history.done {
		return nil, ErrSubmissionsRead
	}

	submissions, err := c.readSubmissionBatch()
	if err != nil {
		return submissions, err
	}

	lastBatch := &PushshiftSubmission{}
	if history.last != nil {
		*lastBatch = *history.last
	}

	for i, submission := range submissions {
		if i == len(submissions) {
			break
		}

		if submission.FullID == lastBatch.FullID {
			submissions = submissions[i+1:]
			break
		}
	}

	// When reading is done, none will be returned or only the submission as the search is made inclusive.
	if len(submissions) == 0 || (len(submissions) == 1 && submissions[0].FullID == lastBatch.FullID) {
		history.last = nil
		history.done = true

		return nil, NewContextlessError(ErrSubmissionsRead).Wrap("all submissions read")
	}

	lastIndex := len(submissions) - 1
	lastSubmission := submissions[lastIndex]
	history.last = &lastSubmission

	if lastSubmission.DateCreated == lastBatch.DateCreated {
		// If this is reached the page is full of submissions of the same epoch.
		// This edge case is likely only relevant on low limits as it's unlikely the maximal limit of 500 with result in posts with the same epoch.
		// Reading is still valid if necessitated so this epoch is skipped.
		history.last.DateCreated++

		return submissions, NewWrappedError("request returned all same epoch", ErrMaySkipSubmissions, []ContextParam{
			{"epoch", fmt.Sprint(lastSubmission.DateCreated)},
		})
	}

	return submissions, nil
}

func (c *Client) readSubmissionBatch() ([]PushshiftSubmission, error) {
	h := c.PushshiftSearch
	config := c.Config
	name := url.QueryEscape(config.Subreddit.Name)

	requestURL := fmt.Sprintf("%s?subreddit=%s&limit=%d", config.Pushshift.URL, name, config.Subreddit.Limit)

	if h.last != nil {
		// Makes the request include the last recorded epoch, for the edge case of the last request ending with an epoch in which there are still more comments of the same epoch right after, outside the search limit.
		requestURL += fmt.Sprintf("&before=%d", int64(h.last.DateCreated)+1)
	}

	b, ce := c.doRequest("GET", requestURL, nil, nil)
	if ce != nil {
		return nil, ce.Wrap("reading failed")
	}

	var responseJSON PushshiftData
	if err := json.Unmarshal(b, &responseJSON); err != nil {
		return nil, NewContextError(err, []ContextParam{
			{"requestURL", requestURL},
			{"responseData", fmt.Sprintf(`"%s"`, b)},
		})
	}

	return responseJSON.Data, nil
}
