package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type toolModel struct {
	Name       types.String         `tfsdk:"name"`
	URL        types.String         `tfsdk:"url"`
	Sample     types.String         `tfsdk:"sample"`
	CLI        *toolCLIModel        `tfsdk:"cli"`
	Connect    *toolConnectModel    `tfsdk:"connect"`
	Credential *toolCredentialModel `tfsdk:"credential"`
	// The credential's value: write-only, so never in plan or state.
	CredentialWO        types.String `tfsdk:"credential_wo"`
	CredentialWOVersion types.Int64  `tfsdk:"credential_wo_version"`
	Version             types.Int64  `tfsdk:"version"`
}

type toolCredentialModel struct {
	Kind     types.String `tfsdk:"kind"`
	Header   types.String `tfsdk:"header"`
	TokenURL types.String `tfsdk:"token_url"`
	ClientID types.String `tfsdk:"client_id"`
	Scopes   []string     `tfsdk:"scopes"`
}

type toolCLIModel struct {
	Hosts []toolHostModel   `tfsdk:"hosts"`
	Allow []string          `tfsdk:"allow"`
	Env   map[string]string `tfsdk:"env"`
}

type toolHostModel struct {
	Host     types.String       `tfsdk:"host"`
	Auth     types.String       `tfsdk:"auth"`
	User     types.String       `tfsdk:"user"`
	Requests []toolRequestModel `tfsdk:"requests"`
	Others   types.String       `tfsdk:"others"`
}

type toolRequestModel struct {
	Method types.String `tfsdk:"method"`
	Path   types.String `tfsdk:"path"`
}

type toolConnectModel struct {
	Provider types.String `tfsdk:"provider"`
	Scopes   []string     `tfsdk:"scopes"`
}

func (m toolModel) spec() glen.ToolSpec {
	s := glen.ToolSpec{Name: m.Name.ValueString(), URL: m.URL.ValueStringPointer(), Sample: m.Sample.ValueStringPointer()}
	if m.Connect != nil {
		s.Connect = &glen.ToolConnect{Provider: m.Connect.Provider.ValueStringPointer(), Scopes: m.Connect.Scopes}
	}
	if c := m.Credential; c != nil {
		s.Credential = &glen.ToolCredential{Kind: c.Kind.ValueString(), Header: c.Header.ValueStringPointer(), TokenURL: c.TokenURL.ValueStringPointer(), ClientID: c.ClientID.ValueStringPointer(), Scopes: c.Scopes}
	}
	if m.CLI != nil {
		s.CLI = &glen.ToolCLI{Allow: m.CLI.Allow, Env: m.CLI.Env}
		for _, h := range m.CLI.Hosts {
			host := glen.ToolHost{Host: h.Host.ValueString()}
			if h.Auth.ValueString() == "basic" {
				host.BasicUser = h.User.ValueStringPointer()
			}
			for _, r := range h.Requests {
				host.Requests = append(host.Requests, glen.ToolRequest{Method: r.Method.ValueString(), Path: r.Path.ValueString()})
			}
			if o := h.Others.ValueString(); o != "" && o != "refuse" {
				host.Others = &o
			}
			s.CLI.Hosts = append(s.CLI.Hosts, host)
		}
	}
	return s.Normalized()
}

// toolModelOf is t as a configuration that says no more than it must
// would read: bearer and no scopes are left unset.
func toolModelOf(t glen.Tool) toolModel {
	s := t.Spec
	m := toolModel{
		Name:    types.StringValue(s.Name),
		URL:     types.StringPointerValue(s.URL),
		Sample:  types.StringPointerValue(s.Sample),
		Version: types.Int64Value(t.Version),
		// Glen never answers the value, nor which version of it was set.
		CredentialWO:        types.StringNull(),
		CredentialWOVersion: types.Int64Null(),
	}
	if c := s.Credential; c != nil {
		m.Credential = &toolCredentialModel{Kind: types.StringValue(c.Kind), Header: types.StringPointerValue(c.Header), TokenURL: types.StringPointerValue(c.TokenURL), ClientID: types.StringPointerValue(c.ClientID), Scopes: nonEmpty(c.Scopes)}
	}
	if s.Connect != nil {
		m.Connect = &toolConnectModel{Provider: types.StringPointerValue(s.Connect.Provider), Scopes: nonEmpty(s.Connect.Scopes)}
	}
	if s.CLI != nil {
		m.CLI = &toolCLIModel{Allow: nonEmpty(s.CLI.Allow)}
		if len(s.CLI.Env) > 0 {
			m.CLI.Env = s.CLI.Env
		}
		for _, h := range s.CLI.Hosts {
			host := toolHostModel{Host: types.StringValue(h.Host), Auth: types.StringNull(), User: types.StringNull(), Others: types.StringPointerValue(h.Others)}
			if h.BasicUser != nil {
				host.Auth, host.User = types.StringValue("basic"), types.StringValue(*h.BasicUser)
			}
			for _, r := range h.Requests {
				host.Requests = append(host.Requests, toolRequestModel{Method: types.StringValue(r.Method), Path: types.StringValue(r.Path)})
			}
			m.CLI.Hosts = append(m.CLI.Hosts, host)
		}
	}
	return m
}

// refreshed keeps m, as written, while Glen holds the spec it says.
func (m toolModel) refreshed(t glen.Tool) toolModel {
	if m.spec().Equal(t.Spec) {
		m.Version = types.Int64Value(t.Version)
		return m
	}
	out := toolModelOf(t)
	out.CredentialWOVersion = m.CredentialWOVersion
	return out
}

// validToolName is what Glen takes as a tool's name: kebab-case, and not
// glen, the name of Glen's own MCP server.
func validToolName(n string) bool {
	ok := n != "" && n != "glen"
	for _, c := range n {
		ok = ok && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')
	}
	return ok
}

// hostProblem is why Glen would refuse h as a cli host, or "": the
// token goes to one named host, written as a lowercase hostname.
func hostProblem(h string) string {
	if strings.HasPrefix(h, "*.") {
		return "must be one hostname: the token goes to named hosts only"
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		ok := l != "" && len(l) <= 63 && !strings.HasPrefix(l, "-") && !strings.HasSuffix(l, "-")
		for _, c := range l {
			ok = ok && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')
		}
		if !ok || len(h) > 253 {
			return "must be a lowercase hostname, such as api.github.com, with no scheme, port or path"
		}
	}
	if len(labels) < 2 {
		return "must have a dot: a single-label name resolves only on a private network"
	}
	if last := labels[len(labels)-1]; strings.Trim(last, "0123456789") == "" {
		return "is an IP address; name the hostname"
	}
	return ""
}

type toolResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure      = &toolResource{}
	_ resource.ResourceWithImportState    = &toolResource{}
	_ resource.ResourceWithValidateConfig = &toolResource{}
)

func newToolResource() resource.Resource { return &toolResource{} }

func (r *toolResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tool"
}

func (r *toolResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A tool of the org, which agents name in tools.mcp: an MCP server at a url, a sample Glen answers itself, or a service the agents' CLIs call. " +
			"Glen keeps it behind /v1/mcp-servers/{name}. Every apply that changes it is a new version in Glen. " +
			"People and agents sign in to a tool in the console; the resource declares the tool, not anyone's connection.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "The tool's name, kebab-case and not glen. Agents name it in tools.mcp. Changing it replaces the tool.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"url": schema.StringAttribute{
				Description: "Where the MCP server answers, http or https. Set exactly one of url, sample and cli.",
				Optional:    true,
			},
			"sample": schema.StringAttribute{
				Description: "A sample MCP server Glen answers itself, such as meetings. Set exactly one of url, sample and cli.",
				Optional:    true,
			},
			"version": schema.Int64Attribute{
				Description: "The tool's version in Glen after the last apply or refresh.",
				Computed:    true,
			},
			"credential_wo": schema.StringAttribute{
				Description: "The credential's value: the API key, or the OAuth2 client's secret. Write-only: Terraform sends it to Glen and keeps it in neither plan nor state, and Glen keeps it in the org's vault and never answers it. " +
					"It is written when the tool is created and whenever credential_wo_version changes. Leave it out to set the value on the tool's page in the console. Needs the credential block and Terraform 1.11 or later.",
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
			},
			"credential_wo_version": schema.Int64Attribute{
				Description: "Change it to write credential_wo again, such as to rotate the key. Terraform compares it, never the value.",
				Optional:    true,
			},
		},
		Blocks: map[string]schema.Block{
			"cli": schema.SingleNestedBlock{
				Description: "A service the agents' CLIs call. Glen's egress proxy adds the connection's token on its hosts, so the token never enters the sandbox. Needs connect with a provider.",
				Attributes: map[string]schema.Attribute{
					"hosts": schema.ListNestedAttribute{
						Description: "The hosts the proxy adds the token to, each one hostname.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"host": schema.StringAttribute{Description: "A lowercase hostname, such as api.github.com.", Required: true},
								"auth": schema.StringAttribute{
									Description: "How the token is written into Authorization: bearer, or basic with user, as git over HTTPS signs in. Unset is bearer.",
									Optional:    true,
								},
								"user": schema.StringAttribute{Description: "The Basic user, with auth = \"basic\", such as x-access-token.", Optional: true},
								"others": schema.StringAttribute{
									Description: "With requests: what Glen does with a request they do not name. refuse (unset) answers 403; anonymous sends it with no token, as anyone may ask.",
									Optional:    true,
								},
								"requests": schema.ListNestedAttribute{
									Description: "When set, the only requests Glen's proxy sends on this host with the token; others says what becomes of the rest. Paths compare without case; * is within one segment and a final ** is the rest of the path.",
									Optional:    true,
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"method": schema.StringAttribute{Description: "GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS.", Required: true},
											"path":   schema.StringAttribute{Description: "A path pattern, such as /repos/acme/app/**.", Required: true},
										},
									},
								},
							},
						},
					},
					"allow": schema.ListAttribute{
						Description: "Hosts of the service the sandbox reaches untouched, such as *.githubusercontent.com.",
						ElementType: types.StringType,
						Optional:    true,
					},
					"env": schema.MapAttribute{
						Description: "Variables set in the sandbox to a stand-in, for a client that will not run without a token, such as GH_TOKEN.",
						ElementType: types.StringType,
						Optional:    true,
					},
				},
			},
			"credential": schema.SingleNestedBlock{
				Description: "The tool's own credential, which Glen adds to every call an agent makes to it as the service, outside the sandbox. The block names its kind; the value is credential_wo, or is set on the tool's page in the console. " +
					"Removing the block, or changing its kind, deletes the value from the org's vault.",
				Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{
						Description: "api-key: the value is sent as it is in header, or as a Bearer token. service: the value is an OAuth2 client's secret, which Glen trades at token_url for a token by the client credentials grant.",
						Optional:    true,
					},
					"header": schema.StringAttribute{
						Description: "api-key only: the header the key is sent in, such as DD-API-KEY. Unset, it is sent as Authorization: Bearer.",
						Optional:    true,
					},
					"token_url": schema.StringAttribute{Description: "service only: the OAuth2 token endpoint, http or https.", Optional: true},
					"client_id": schema.StringAttribute{Description: "service only: the OAuth2 client whose secret the value is.", Optional: true},
					"scopes": schema.ListAttribute{
						Description: "service only: the scopes the token is asked for.",
						ElementType: types.StringType,
						Optional:    true,
					},
				},
			},
			"connect": schema.SingleNestedBlock{
				Description: "People and agents sign in to the tool, in the console. Without a provider, Glen finds the server's sign-in and registers itself there when the tool is created.",
				Attributes: map[string]schema.Attribute{
					"provider": schema.StringAttribute{
						Description: "An AgentCore Identity OAuth2 provider made outside Glen. Unset, Glen makes its own. A cli tool needs one.",
						Optional:    true,
					},
					"scopes": schema.ListAttribute{
						Description: "The scopes a connection asks for. Changing them means everyone connects again.",
						ElementType: types.StringType,
						Optional:    true,
					},
				},
			},
		},
	}
}

func (r *toolResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// ValidateConfig checks what Glen would refuse of the tool's shape. Glen
// checks the rest at apply.
func (r *toolResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var name, url, sample, wo types.String
	var cli, connect, credential types.Object
	for p, v := range map[string]any{"name": &name, "url": &url, "sample": &sample, "cli": &cli, "connect": &connect, "credential": &credential, "credential_wo": &wo} {
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(p), v)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if !name.IsUnknown() && !name.IsNull() && !validToolName(name.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid tool name",
			fmt.Sprintf("Tool name %q must be kebab-case and not glen.", name.ValueString()))
	}
	kinds := []attr.Value{url, sample, cli}
	set := 0
	for _, k := range kinds {
		if k.IsUnknown() {
			return
		}
		if !k.IsNull() {
			set++
		}
	}
	if set != 1 {
		resp.Diagnostics.AddError("Invalid tool",
			fmt.Sprintf("A glen_tool sets exactly one of url, sample and a cli block; this one sets %d.", set))
	}
	if !sample.IsNull() && !connect.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("connect"), "Invalid tool", "A sample needs no sign-in; leave connect out.")
	}
	if credential.IsNull() && !wo.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("credential_wo"), "Missing credential block",
			"credential_wo is the value of the tool's credential: add a credential block naming its kind.")
	}
	if !credential.IsNull() && !credential.IsUnknown() {
		if url.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("credential"), "Invalid tool", "A credential is added to calls to an MCP server at a url: a sample needs none, and a cli tool uses its connection's token.")
		}
		validateCredential(credential, &resp.Diagnostics)
	}
	if cli.IsNull() {
		return
	}
	if connect.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("connect"), "Missing connect block",
			"A cli tool needs connect with a provider: the sign-in whose token Glen adds for the CLI.")
	} else if p, ok := connect.Attributes()["provider"]; ok && p.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("connect").AtName("provider"), "Missing provider",
			"A CLI's service has no sign-in Glen can set up itself: name an Identity OAuth2 provider in connect.provider.")
	}
	hosts, ok := cli.Attributes()["hosts"].(types.List)
	if !ok || hosts.IsUnknown() {
		return
	}
	if len(hosts.Elements()) == 0 {
		resp.Diagnostics.AddAttributeError(path.Root("cli").AtName("hosts"), "Missing hosts",
			"cli.hosts is empty: list the hosts Glen adds the token to, such as api.github.com.")
	}
	for i, e := range hosts.Elements() {
		r.validateHost(e, path.Root("cli").AtName("hosts").AtListIndex(i), &resp.Diagnostics)
	}
}

// validateCredential checks what Glen would refuse of a credential block.
func validateCredential(c types.Object, diags *diag.Diagnostics) {
	a := c.Attributes()
	at := path.Root("credential")
	kind, _ := a["kind"].(types.String)
	if kind.IsUnknown() {
		return
	}
	set := func(name string) bool {
		v, ok := a[name]
		return ok && !v.IsNull()
	}
	switch kind.ValueString() {
	case "api-key":
		for _, f := range []string{"token_url", "client_id", "scopes"} {
			if set(f) {
				diags.AddAttributeError(at.AtName(f), "Invalid credential", f+" is a service credential's; an api-key credential has a header at most.")
			}
		}
	case "service":
		if set("header") {
			diags.AddAttributeError(at.AtName("header"), "Invalid credential", "A service credential's token is sent as Authorization: Bearer; leave header out.")
		}
		for _, f := range []string{"token_url", "client_id"} {
			if !set(f) {
				diags.AddAttributeError(at.AtName(f), "Invalid credential", "A service credential needs "+f+": the OAuth2 client Glen trades the value for a token as.")
			}
		}
	case "":
		diags.AddAttributeError(at.AtName("kind"), "Invalid credential", "credential.kind is missing: api-key or service.")
	default:
		diags.AddAttributeError(at.AtName("kind"), "Invalid credential", fmt.Sprintf("credential.kind %q is not one Glen has; it has api-key and service.", kind.ValueString()))
	}
}

func (r *toolResource) validateHost(e attr.Value, at path.Path, diags *diag.Diagnostics) {
	obj, ok := e.(types.Object)
	if !ok || obj.IsUnknown() {
		return
	}
	a := obj.Attributes()
	host, _ := a["host"].(types.String)
	auth, _ := a["auth"].(types.String)
	user, _ := a["user"].(types.String)
	if !host.IsUnknown() && !host.IsNull() {
		if why := hostProblem(host.ValueString()); why != "" {
			diags.AddAttributeError(at.AtName("host"), "Invalid host", fmt.Sprintf("cli.hosts %q %s.", host.ValueString(), why))
		}
	}
	if auth.IsUnknown() || user.IsUnknown() {
		return
	}
	switch auth.ValueString() {
	case "", "bearer":
		if !user.IsNull() {
			diags.AddAttributeError(at.AtName("user"), "Invalid host", "user goes with auth = \"basic\"; a bearer token has no user.")
		}
	case "basic":
		if u := user.ValueString(); u == "" || strings.Contains(u, ":") {
			diags.AddAttributeError(at.AtName("user"), "Invalid host", "auth = \"basic\" needs a user that is not empty and has no colon, such as x-access-token.")
		}
	default:
		diags.AddAttributeError(at.AtName("auth"), "Invalid host", fmt.Sprintf("auth %q is not one Glen has; it has bearer and basic.", auth.ValueString()))
	}
}

func (r *toolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m toolModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	name := m.Name.ValueString()
	if _, err := r.client.GetTool(ctx, name); err == nil {
		resp.Diagnostics.AddError("Tool already exists",
			fmt.Sprintf("Glen already has a tool named %q. Import it with `terraform import <address> %s` to manage it here.", name, name))
		return
	} else if !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Reading tool "+name, glenMessage(err))
		return
	}
	r.put(ctx, m, req.Config, true, nil, &resp.State, &resp.Diagnostics)
}

func (r *toolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, was toolModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &was)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	r.put(ctx, m, req.Config, !m.CredentialWOVersion.Equal(was.CredentialWOVersion), &was, &resp.State, &resp.Diagnostics)
}

// put writes the tool, then, when write is true and the configuration
// holds credential_wo, its credential's value. Glen sends a value only
// where the record named when it was set, so a change of the url or of
// the credential's kind, header, token_url or client_id that comes with a
// new value removes the old one first; one that comes without a new value
// is Glen's to refuse while a value is set.
func (r *toolResource) put(ctx context.Context, m toolModel, config tfsdk.Config, write bool, was *toolModel, state *tfsdk.State, diags *diag.Diagnostics) {
	name := m.Name.ValueString()
	var wo types.String
	diags.Append(config.GetAttribute(ctx, path.Root("credential_wo"), &wo)...)
	if diags.HasError() {
		return
	}
	writing := write && !wo.IsNull() && !wo.IsUnknown()
	if was != nil {
		if field := was.spec().CredentialMoves(m.spec()); field != "" {
			if !writing {
				diags.AddAttributeWarning(path.Root("credential_wo_version"), "Tool "+name+"'s credential moves",
					fmt.Sprintf("This apply changes %s. Glen refuses that while the credential is set; give the value in credential_wo and bump credential_wo_version in the same apply, and the provider removes it, changes the tool and sets it again.", field))
			} else if err := r.client.RemoveToolCredential(ctx, name); err != nil && !glen.IsNotFound(err) {
				diags.AddError("Glen refused to remove tool "+name+"'s credential before changing "+field, glenMessage(err))
				return
			}
		}
	}
	t, err := r.client.PutTool(ctx, m.spec())
	if err != nil {
		diags.AddError("Glen refused tool "+name, glenMessage(err))
		return
	}
	m.Version = types.Int64Value(t.Version)
	m.CredentialWO = types.StringNull()
	if writing {
		if err := r.client.SetToolCredential(ctx, name, wo.ValueString()); err != nil {
			// The tool is written; the next apply sets the value again.
			m.CredentialWOVersion = types.Int64Null()
			diags.Append(state.Set(ctx, m)...)
			diags.AddError("Glen refused tool "+name+"'s credential", glenMessage(err))
			return
		}
	}
	diags.Append(state.Set(ctx, m)...)
}

func (r *toolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m toolModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	t, err := r.client.GetTool(ctx, m.Name.ValueString())
	if glen.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading tool "+m.Name.ValueString(), glenMessage(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m.refreshed(t))...)
}

func (r *toolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if err := r.client.DeleteTool(ctx, name.ValueString()); err != nil && !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Glen refused to delete tool "+name.ValueString(), glenMessage(err))
	}
}

func (r *toolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
