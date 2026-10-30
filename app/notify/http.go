package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
// errors never contain target: a Slack, Discord or Teams webhook URL is
// the secret, and an error ends up in logs and on the page.
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

	return statusError(resp, excerpt)
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

	message := "the provider answered " + resp.Status
	if reason != "" {
		message += ": " + reason
	}
	after := resp.Header.Get("Retry-After")
	if _, err := strconv.ParseFloat(after, 64); resp.StatusCode == http.StatusTooManyRequests && err == nil {
		message += " (try again in " + after + "s)"
	}

	return errors.New(message)
}
