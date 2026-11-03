// Package newsletter signs an address up for Kstack's newsletter.
package newsletter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DefaultURL is a placeholder until the newsletter backend exists.
const DefaultURL = "https://kstack.sh/api/newsletter"

// Client posts sign-ups to URL.
type Client struct {
	URL  string
	HTTP *http.Client
}

// New returns a client for DefaultURL.
func New() Client {
	return Client{URL: DefaultURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Subscribe signs email up. Any status but 2xx is an error.
func (c Client) Subscribe(ctx context.Context, email string) error {
	body, _ := json.Marshal(map[string]string{"email": email}) // a map of strings always marshals
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("newsletter: the server answered %s", resp.Status)
	}
	return nil
}
