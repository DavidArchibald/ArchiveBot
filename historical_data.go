package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"
)

// History is a structure to traverse the history of a subreddit's submissions.
type History struct {
	config *Config
	last   *PushshiftSubmission
	done   bool
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

// PushshiftFields are the extracted fields of pushshift
type PushshiftFields struct {
	ID    string `json:"id"`
	Epoch int64  `json:"created_utc"`
}

// UnmarshalJSON from bytes.
func (s *PushshiftSubmission) UnmarshalJSON(b []byte) error {
	var fields PushshiftFields
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	*s = PushshiftSubmission{fields, raw}
	return nil
}

// MarshalJSON turns into a submission into JSON.
func (s *PushshiftSubmission) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Raw)
}

// MarshalBinary turns the json string into raw bytes.
func (s PushshiftSubmission) MarshalBinary() ([]byte, error) {
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(s.Raw); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// NewHistory constructs a history object from the configuration.
func NewHistory(config *Config) *History {
	return &History{config: config}
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

// ReadAllSubmissions gets every submission with an appropriate delay between.
func (h *History) ReadAllSubmissions() func() ([]PushshiftSubmission, error) {
	return func() ([]PushshiftSubmission, error) {
		submissions, err := h.ReadSubmissionBatch()
		if err != nil && !errors.Is(err, ErrSubmissionsRead) {
			return nil, err
		}

		if h.done && submissions == nil {
			return nil, nil
		}

		time.Sleep(time.Duration(h.config.Pushshift.Delay))

		return submissions, nil
	}
}

// ReadSubmissionBatch reads a batch of submissions from Pushshift.
// ReadSubmissionBatch will panic if given a limit less than 2. There is no known downside to putting the limit to the current maximum of 500, the minimum suggested is 10.
// The length returned will be less than or equal to the limit, usually about the limit - 1 after the first request. This is to prevent submissions being skipped.
// If the error is the sentinel error ErrSubmissionsRead, any submissions are still valid. Otherwise if a submission is returned with an error, it is unintended behavior.
func (h *History) ReadSubmissionBatch() ([]PushshiftSubmission, error) {
	if h.config.Subreddit.Limit <= 2 {
		panic(fmt.Errorf("expected limit to be at least 2: %w", ErrInvalidLimit))
	}

	if h.done {
		return nil, ErrSubmissionsRead
	}

	submissions, err := h.readSubmissionBatch()
	if err != nil {
		return submissions, err
	}

	lastBatch := &PushshiftSubmission{}
	if h.last != nil {
		*lastBatch = *h.last
	}

	for i, submission := range submissions {
		if i == len(submissions) {
			break
		}

		if submission.ID == lastBatch.ID {
			submissions = submissions[i+1:]
			break
		}
	}

	// When reading is done none, will be returned or only the submission as the search is made inclusive.
	if len(submissions) == 0 {
		h.last = nil
		h.done = true
		return nil, ErrSubmissionsRead
	}

	lastIndex := len(submissions) - 1
	lastSubmission := submissions[lastIndex]
	h.last = &lastSubmission

	if lastSubmission.Epoch == lastBatch.Epoch {
		fmt.Println(lastSubmission.Epoch, "\n", "\n", "\n", lastBatch.Epoch)
		// If this is reached the page is full of submissions of the same epoch.
		// This edge case is likely only relevant on low limits as it's unlikely the maximal limit of 500 with result in posts with the same epoch.
		// Reading is still valid if necessitated so this epoch is skipped.
		h.last.Epoch++

		return submissions, fmt.Errorf("request returned all same epoch: %w", ErrMaySkipSubmissions)
	}

	return submissions, nil
}

func (h *History) readSubmissionBatch() ([]PushshiftSubmission, error) {
	config := h.config
	name := url.QueryEscape(config.Subreddit.Name)

	requestURL := fmt.Sprintf("%s?subreddit=%s&limit=%d", config.Pushshift.URL, name, config.Subreddit.Limit)

	if h.last != nil {
		// Makes the request include the last recorded epoch, for the edge case of the last request ending with an epoch in which there are still more comments of the same epoch right after, outside the request limit.
		requestURL += fmt.Sprintf("&before=%d", h.last.Epoch+1)
	}

	resp, err := http.Get(requestURL)
	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var responseJSON PushshiftData
	if err := json.Unmarshal(b, &responseJSON); err != nil {
		return nil, err
	}

	return responseJSON.Data, nil
}
