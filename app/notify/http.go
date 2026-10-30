package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// client never follows a redirect: a webhook that answers with one would
// have the message sent somewhere nobody chose.
var client = &http.Client{
	Timeout: Timeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// postJSON sends body to target and fails on anything but a 2xx. Its
// errors never contain target, even when the answer quotes it: a Slack,
// Discord or Teams webhook URL is the secret, and an error ends up in
// logs and on the page.
func postJSON(ctx context.Context, target string, body any, header http.Header) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return errors.New("the URL isn't valid")
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Echowa")

	resp, err := client.Do(req)
	if err != nil {
		return withoutURL(err)
	}
	defer resp.Body.Close()
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 == 2 {
		return nil
	}

	return statusError(resp, redact(excerpt, req.URL))
}

// redact blanks target in a provider's answer, and its path and query on
// their own, which hold a webhook's secret: some echo the request back.
func redact(body []byte, target *url.URL) []byte {
	for _, secret := range []string{target.String(), target.EscapedPath(), target.Path, target.RawQuery} {
		if len(secret) > 1 {
			body = bytes.ReplaceAll(body, []byte(secret), []byte("[redacted]"))
		}
	}

	return body
}

// withoutURL replaces a *url.Error, which prints the whole URL, with what
// went wrong.
func withoutURL(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	if ue.Timeout() {
		return fmt.Errorf("the provider didn't answer within %s", Timeout)
	}

	return ue.Err
}

// StatusError is a provider's answer that wasn't a 2xx.
type StatusError struct {
	Code int
	// RetryAfter is how long a 429 asked to wait, or 0.
	RetryAfter time.Duration
	message    string
}

func (e *StatusError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("the provider answered %d", e.Code)
	}

	return e.message
}

// Permanent says whether trying err again can't help: the provider
// refused the message itself, as a deleted webhook's 404 does. A rate
// limit, a timeout and a server error are worth another try, and so is
// anything that never reached the provider.
func Permanent(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code/100 == 4 && se.Code != http.StatusRequestTimeout && se.Code != http.StatusTooManyRequests
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code/100 == 5
	}

	return false
}

// RetryAfter is how long the provider asked to wait before the next try,
// or 0.
func RetryAfter(err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}

	return 0
}

// statusError says what the provider answered, with its own message when
// its body has one: Slack's is plain text, Discord's, ntfy's and Resend's
// are JSON with a message or error field.
func statusError(resp *http.Response, body []byte) error {
	reason := strings.TrimSpace(string(body))
	var parsed struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		switch e := parsed.Error.(type) {
		case string:
			reason = e
		case map[string]any:
			if m, ok := e["message"].(string); ok {
				reason = m
			}
		}
		if parsed.Message != "" {
			reason = parsed.Message
		}
	}
	reason = truncate(strings.Join(strings.Fields(reason), " "), 200)

	se := &StatusError{Code: resp.StatusCode, message: "the provider answered " + resp.Status}
	if reason != "" {
		se.message += ": " + reason
	}
	after := resp.Header.Get("Retry-After")
	if seconds, err := strconv.ParseFloat(after, 64); resp.StatusCode == http.StatusTooManyRequests && err == nil && seconds >= 0 {
		se.RetryAfter = time.Duration(seconds * float64(time.Second))
		se.message += " (try again in " + after + "s)"
	}

	return se
}
