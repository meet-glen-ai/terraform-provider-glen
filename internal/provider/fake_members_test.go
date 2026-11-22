package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"testing"

	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

// fakes maps each fake's URL to it, and owners each real Glen's URL to
// its database owner's URL, so a test can seed members as WorkOS and
// sign-in would.
var fakes, owners sync.Map

// fakeMember is a member or an invitation, as Glen with WorkOS answers.
type fakeMember struct {
	role  string
	state string
}

func (f *fakeGlen) memberRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/members/{email}", f.getMember)
	mux.HandleFunc("PUT /v1/members/{email}", f.putMember)
	mux.HandleFunc("DELETE /v1/members/{email}", f.deleteMember)
}

func (f *fakeGlen) member(email string) glen.Member {
	m := f.members[email]
	role := m.role
	return glen.Member{Email: email, Role: &role, State: m.state}
}

func (f *fakeGlen) admins() int {
	n := 0
	for _, m := range f.members {
		if m.state == "active" && m.role == "admin" {
			n++
		}
	}
	return n
}

func (f *fakeGlen) getMember(w http.ResponseWriter, r *http.Request) {
	email := r.PathValue("email")
	if f.members[email] == nil {
		fail(w, http.StatusNotFound, email+" is neither a member of this org nor invited to it")
		return
	}
	answer(w, f.member(email))
}

func (f *fakeGlen) putMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Role != "admin" && body.Role != "member") {
		fail(w, http.StatusBadRequest, fmt.Sprintf("%q is not a role; a role is admin or member", body.Role))
		return
	}
	email := r.PathValue("email")
	m := f.members[email]
	switch {
	case m == nil:
		f.members[email] = &fakeMember{role: body.Role, state: "invited"}
	case m.state == "active" && m.role == "admin" && body.Role != "admin" && f.admins() <= 1:
		fail(w, http.StatusConflict, email+" is the org's last admin; make another member an admin first")
		return
	default:
		m.role = body.Role
	}
	answer(w, f.member(email))
}

func (f *fakeGlen) deleteMember(w http.ResponseWriter, r *http.Request) {
	email := r.PathValue("email")
	m := f.members[email]
	switch {
	case m == nil:
		fail(w, http.StatusNotFound, email+" is not a member of this org")
		return
	case m.state == "active" && m.role == "admin" && f.admins() <= 1:
		fail(w, http.StatusConflict, email+" is the org's last admin; make another member an admin first")
		return
	}
	delete(f.members, email)
	w.WriteHeader(http.StatusNoContent)
}

// seedMember makes email an active member with role, as joining through
// WorkOS would: in the fake directly, in a real Glen as its members row.
func (b backend) seedMember(t *testing.T, email, role string) {
	t.Helper()
	if !b.real {
		v, _ := fakes.Load(b.url)
		f := v.(*fakeGlen)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.members[email] = &fakeMember{role: role, state: "active"}
		return
	}
	v, _ := owners.Load(b.url)
	sql := fmt.Sprintf(`WITH u AS (INSERT INTO users (id, workos_user_id, email) VALUES (gen_random_uuid(), 'user_%[1]s', '%[1]s') RETURNING id)
INSERT INTO members (org_id, user_id, name, role) SELECT o.id, u.id, '%[1]s', '%[2]s' FROM orgs o, u WHERE o.name = 'default'`, email, role)
	if out, err := exec.Command("psql", v.(string), "-qAt", "-v", "ON_ERROR_STOP=1", "-c", sql).CombinedOutput(); err != nil {
		t.Fatalf("seeding %s: %v\n%s", email, err, out)
	}
}
