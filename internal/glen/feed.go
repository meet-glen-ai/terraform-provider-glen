package glen

import (
	"context"
	"net/http"
	"net/url"
)

// FeedSpec is a feed the org's agents start on: Glen polls URL every
// PollSecs and sends each new entry to the agents whose trigger names it.
type FeedSpec struct {
	Name     string `json:"name"`
	Format   string `json:"format"`
	URL      string `json:"url"`
	PollSecs int64  `json:"poll_secs"`
	Baseline bool   `json:"baseline"`
}

type Feed struct {
	Version int64    `json:"version"`
	Spec    FeedSpec `json:"spec"`
}

func (c *Client) GetFeed(ctx context.Context, name string) (Feed, error) {
	var f Feed
	err := c.do(ctx, http.MethodGet, "/v1/feeds/"+url.PathEscape(name), nil, &f)
	return f, err
}

func (c *Client) PutFeed(ctx context.Context, spec FeedSpec) (Feed, error) {
	var f Feed
	err := c.do(ctx, http.MethodPut, "/v1/feeds/"+url.PathEscape(spec.Name), spec, &f)
	return f, err
}

// DeleteFeed deletes the feed. Glen refuses while a live agent's trigger
// names it, naming those agents.
func (c *Client) DeleteFeed(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/feeds/"+url.PathEscape(name), nil, nil)
}
