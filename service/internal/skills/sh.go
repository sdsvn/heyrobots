package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// SHURL is skills.sh's search, the public directory of skills; tests
// point it at a fake.
var SHURL = "https://skills.sh/api/search"

// Found is one skill in skills.sh's search. Its ID is owner/repo/name.
type Found struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Name     string `json:"name"`
	Installs int    `json:"installs"`
}

// SearchSH searches skills.sh, most installed first.
func SearchSH(ctx context.Context, q string) ([]Found, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SHURL+"?q="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skills.sh can't be reached: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills.sh answered %s", resp.Status)
	}
	var res struct {
		Skills []Found `json:"skills"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&res); err != nil {
		return nil, fmt.Errorf("skills.sh's answer couldn't be read: %w", err)
	}
	slices.SortStableFunc(res.Skills, func(a, b Found) int { return b.Installs - a.Installs })
	return res.Skills, nil
}
