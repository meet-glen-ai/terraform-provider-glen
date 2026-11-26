package provider

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

// fakeRole is one of the org's own roles, as Glen keeps it.
type fakeRole struct {
	description string
	members     []string
}

func (f *fakeGlen) roleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/roles/{name}", f.getRole)
	mux.HandleFunc("PUT /v1/roles/{name}", f.putRole)
	mux.HandleFunc("DELETE /v1/roles/{name}", f.deleteRole)
}

func (f *fakeGlen) role(name string) glen.Role {
	r := f.roles[name]
	members := slices.Clone(r.members)
	slices.Sort(members)
	return glen.Role{Name: name, Description: r.description, Members: members}
}

// missingApprovers is Glen's refusal of an agent naming roles the org
// does not have, or "" when it has them all.
func (f *fakeGlen) missingApprovers(spec glen.AgentSpec) string {
	var missing []string
	for _, a := range spec.Approvers {
		if a != "admin" && a != "member" && f.roles[a] == nil {
			missing = append(missing, a)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "invalid agent spec: agent " + spec.Agent + " names approver roles the org does not have: " + strings.Join(missing, ", ") + "; an admin makes a role on Settings, Roles"
}

func (f *fakeGlen) getRole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if f.roles[name] == nil {
		fail(w, http.StatusNotFound, "no role named "+name)
		return
	}
	answer(w, f.role(name))
}

func (f *fakeGlen) putRole(w http.ResponseWriter, r *http.Request) {
	var body glen.RoleSpec
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	name := r.PathValue("name")
	if name == "admin" || name == "member" || !roleName.MatchString(name) || len(name) > 40 {
		fail(w, http.StatusBadRequest, name+" is not a role name")
		return
	}
	if body.Members != nil {
		for _, m := range *body.Members {
			if p := f.members[m]; p == nil || p.state != "active" {
				fail(w, http.StatusBadRequest, m+" is not a member of this org")
				return
			}
		}
	}
	role := f.roles[name]
	status := http.StatusOK
	if role == nil {
		role = &fakeRole{}
		f.roles[name] = role
		status = http.StatusCreated
	}
	role.description = strings.TrimSpace(body.Description)
	if body.Members != nil {
		role.members = slices.Clone(*body.Members)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(f.role(name))
}

func (f *fakeGlen) deleteRole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if f.roles[name] == nil {
		fail(w, http.StatusNotFound, "no role named "+name)
		return
	}
	var naming []string
	for _, a := range f.agents {
		if !a.deleted && slices.Contains(a.spec.Approvers, name) {
			naming = append(naming, a.spec.Agent)
		}
	}
	if len(naming) > 0 {
		slices.Sort(naming)
		fail(w, http.StatusConflict, "role "+name+" approves for "+strings.Join(naming, ", ")+"; take it out of their approvers first")
		return
	}
	delete(f.roles, name)
	w.WriteHeader(http.StatusNoContent)
}
