package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type fakeFeed struct {
	version int64
	spec    glen.FeedSpec
	deleted bool
}

func (f *fakeGlen) feedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/feeds/{name}", f.getFeed)
	mux.HandleFunc("PUT /v1/feeds/{name}", f.putFeed)
	mux.HandleFunc("DELETE /v1/feeds/{name}", f.deleteFeed)
}

func (f *fakeGlen) liveFeed(name string) bool {
	fd := f.feeds[name]
	return fd != nil && !fd.deleted
}

func (f *fakeGlen) getFeed(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !f.liveFeed(name) {
		fail(w, http.StatusNotFound, "no feed named "+name)
		return
	}
	fd := f.feeds[name]
	answer(w, glen.Feed{Version: fd.version, Spec: fd.spec})
}

// putFeed answers as Glen does: defaults for poll_secs and baseline, and
// the problems Glen names in words.
func (f *fakeGlen) putFeed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     *string `json:"name"`
		Format   *string `json:"format"`
		URL      *string `json:"url"`
		PollSecs *int64  `json:"poll_secs"`
		Baseline *bool   `json:"baseline"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid feed spec: "+err.Error())
		return
	}
	name := r.PathValue("name")
	spec := glen.FeedSpec{Name: name, PollSecs: 3600, Baseline: true}
	if body.Name != nil {
		spec.Name = *body.Name
	}
	if body.Format != nil {
		spec.Format = *body.Format
	}
	if body.URL != nil {
		spec.URL = *body.URL
	}
	if body.PollSecs != nil {
		spec.PollSecs = *body.PollSecs
	}
	if body.Baseline != nil {
		spec.Baseline = *body.Baseline
	}
	var problems []string
	if spec.Name != name {
		problems = append(problems, fmt.Sprintf("the spec names feed %q, the path %q", spec.Name, name))
	}
	if !kebab(spec.Name) {
		problems = append(problems, fmt.Sprintf("feed name %q must be kebab-case", spec.Name))
	}
	if !slices.Contains([]string{"osv", "cisa-kev", "rss"}, spec.Format) {
		problems = append(problems, fmt.Sprintf("format %q must be one of osv, cisa-kev, rss", spec.Format))
	}
	if !strings.HasPrefix(spec.URL, "https://") {
		problems = append(problems, fmt.Sprintf("url %q must be an https URL", spec.URL))
	}
	if spec.PollSecs < 60 || spec.PollSecs > 86400 {
		problems = append(problems, fmt.Sprintf("poll_secs %d must be from 60 to 86400", spec.PollSecs))
	}
	if len(problems) > 0 {
		fail(w, http.StatusBadRequest, "invalid feed spec: "+strings.Join(problems, "; "))
		return
	}
	fd := f.feeds[name]
	if fd == nil {
		fd = &fakeFeed{}
		f.feeds[name] = fd
	}
	if fd.deleted || fd.version == 0 || fd.spec != spec {
		fd.version++
		fd.spec, fd.deleted = spec, false
	}
	answer(w, glen.Feed{Version: fd.version, Spec: fd.spec})
}

func (f *fakeGlen) deleteFeed(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !f.liveFeed(name) {
		fail(w, http.StatusNotFound, "no feed named "+name)
		return
	}
	var naming []string
	for agent, a := range f.agents {
		if a.deleted {
			continue
		}
		for _, t := range a.spec.Trigger {
			if t.Feed != nil && *t.Feed == name {
				naming = append(naming, agent)
			}
		}
	}
	if len(naming) > 0 {
		slices.Sort(naming)
		fail(w, http.StatusConflict, fmt.Sprintf("feed %s starts %s; change or delete those agents first", name, strings.Join(naming, ", ")))
		return
	}
	f.feeds[name].version++
	f.feeds[name].deleted = true
	w.WriteHeader(http.StatusNoContent)
}
