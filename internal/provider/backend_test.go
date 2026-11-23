package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type backend struct {
	url  string
	key  string
	api  *glen.Client
	real bool
}

func eachBackend(t *testing.T, f func(t *testing.T, b backend)) {
	t.Run("fake", func(t *testing.T) {
		t.Parallel()
		key := randomKey(t)
		f(t, newBackend(startFake(t, key), key, false))
	})
	t.Run("glen", func(t *testing.T) {
		t.Parallel()
		bin, server := os.Getenv("GLEN_BIN"), os.Getenv("DATABASE_URL")
		if os.Getenv("TF_ACC") == "" || bin == "" || server == "" {
			t.Skip("set TF_ACC=1, GLEN_BIN to a glen binary and DATABASE_URL to a Postgres server, such as postgres://postgres@127.0.0.1:5432/postgres, to run against glen serve")
		}
		key := randomKey(t)
		f(t, newBackend(startGlen(t, bin, server, key), key, true))
	})
}

func newBackend(url, key string, real bool) backend {
	return backend{url: url, key: key, api: glen.NewClient(url, key), real: real}
}

func (b backend) test(t *testing.T, c resource.TestCase) {
	c.ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
		"glen": providerserver.NewProtocol6WithError(New("test")()),
	}
	if b.real {
		resource.Test(t, c)
	} else {
		resource.UnitTest(t, c)
	}
}

func (b backend) config(resources string) string {
	return fmt.Sprintf("provider \"glen\" {\n  url     = %q\n  api_key = %q\n}\n\n%s", b.url, b.key, resources)
}

var toolsEnv = []string{"GITHUB_TOKEN"}

// database makes a database of the test's own on the Postgres server at
// server, owned by glen_owner, which like RDS's master user is neither
// superuser nor BYPASSRLS, and drops it after the test. It answers the
// owner's URL, which glen migrate logs in with, and the URL glen serve
// logs in with.
func database(t *testing.T, server string) (owner, app string) {
	psql := func(sql string) {
		out, err := exec.Command("psql", server, "-qAt", "-v", "ON_ERROR_STOP=1", "-c", sql).CombinedOutput()
		if err != nil {
			t.Fatalf("psql %s: %v\n%s", sql, err, out)
		}
	}
	psql(`DO $$ BEGIN CREATE ROLE glen_owner LOGIN NOSUPERUSER NOBYPASSRLS CREATEROLE CREATEDB; EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $$`)
	name := "glen_tf_" + randomKey(t)[:16]
	psql("CREATE DATABASE " + name + " OWNER glen_owner")
	t.Cleanup(func() { psql("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })
	u, err := url.Parse(server)
	if err != nil {
		t.Fatalf("DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	app = u.String()
	u.User = url.User("glen_owner")
	return u.String(), app
}

// fakeVault is a Secrets Manager on loopback that answers the calls
// Glen's vault makes, from names to values in memory, so a glen serve
// keeps the org's secrets without reaching AWS.
type fakeVault struct {
	mu      sync.Mutex
	secrets map[string]string
}

// vaults maps each real Glen's URL to its fake vault.
var vaults sync.Map

func startVault(t *testing.T) (*fakeVault, string) {
	v := &fakeVault{secrets: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SecretId, Name, SecretString string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := body.SecretId + body.Name
		v.mu.Lock()
		defer v.mu.Unlock()
		reply := func(status int, out map[string]any) {
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(out)
		}
		value, ok := v.secrets[name]
		missing := map[string]any{"__type": "ResourceNotFoundException", "message": "no secret " + name}
		arn := "arn:aws:secretsmanager:us-west-2:000000000000:secret:" + name
		switch op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "secretsmanager."); {
		case op == "CreateSecret" && ok:
			reply(400, map[string]any{"__type": "ResourceExistsException", "message": "exists"})
		case op == "CreateSecret":
			v.secrets[name] = body.SecretString
			reply(200, map[string]any{"ARN": arn, "Name": name, "VersionId": "v1"})
		case !ok:
			reply(400, missing)
		case op == "DescribeSecret":
			reply(200, map[string]any{"ARN": arn, "Name": name})
		case op == "GetSecretValue":
			reply(200, map[string]any{"ARN": arn, "Name": name, "SecretString": value, "VersionId": "v1"})
		case op == "PutSecretValue":
			v.secrets[name] = body.SecretString
			reply(200, map[string]any{"ARN": arn, "Name": name, "VersionId": "v2"})
		case op == "DeleteSecret":
			delete(v.secrets, name)
			reply(200, map[string]any{"ARN": arn, "Name": name, "DeletionDate": 0})
		default:
			reply(400, map[string]any{"__type": "InvalidRequestException", "message": "the fake vault does not answer " + op})
		}
	}))
	t.Cleanup(srv.Close)
	return v, srv.URL
}

// holding is each secret the vault holds whose name ends with suffix.
func (v *fakeVault) holding(suffix string) map[string]string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := map[string]string{}
	for name, value := range v.secrets {
		if strings.HasSuffix(name, suffix) {
			out[name] = value
		}
	}
	return out
}

// startUpstream is a model API that takes every key, for Glen's check of
// one an admin sets.
func startUpstream(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func startGlen(t *testing.T, bin, server, adminKey string) string {
	dir := t.TempDir()
	owner, app := database(t, server)
	vault, vaultURL := startVault(t)
	upstream := startUpstream(t)
	// glen reads its settings from GLEN_ variables only, as a Fargate task
	// does; none of this process's own reach it, nor its AWS settings: the
	// vault is the fake, signed with keys AWS refuses.
	env := func(database string, extra ...string) []string {
		var out []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GLEN_") && !strings.HasPrefix(kv, "AWS_") {
				out = append(out, kv)
			}
		}
		return append(append(out,
			"AWS_ENDPOINT_URL_SECRETS_MANAGER="+vaultURL,
			"AWS_REGION=us-west-2",
			"AWS_ACCESS_KEY_ID=tf-test",
			"AWS_SECRET_ACCESS_KEY=tf-test",
			"AWS_EC2_METADATA_DISABLED=true",
			"GLEN_SECRETS_BACKEND=aws",
			"GLEN_SECRETS_PREFIX=tf-test",
			"GLEN_MODEL_UPSTREAM="+upstream,
			"GLEN_MODEL_OPENAI_UPSTREAM="+upstream,
			"GLEN_STORE=file://"+dir+"/store",
			"GLEN_SANDBOX_BACKEND=process",
			"GLEN_SANDBOX_WORKSPACE_ROOT="+dir+"/sessions",
			"GLEN_TOOLS_ENV="+jsonStrings(toolsEnv),
			"GLEN_DATABASE_URL="+database,
		), extra...)
	}
	migrate := exec.Command(bin, "migrate")
	migrate.Env = env(owner)
	if out, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("glen migrate: %v\n%s", err, out)
	}
	// A free port can be taken by another process between freePort closing
	// it and glen binding it, as when several test runs share
	// one machine. Glen then exits with "Address already in use", and is
	// started again on other ports.
	for attempt := 1; ; attempt++ {
		url, taken := serveGlen(t, bin, env(app, "GLEN_DATABASE_SET_ROLE=glen_app", "GLEN_BOOTSTRAP_ADMIN_TOKEN="+adminKey, "GLEN_BOOTSTRAP_ADMIN_ENABLED=true"), adminKey)
		if !taken {
			owners.Store(url, owner)
			vaults.Store(url, vault)
			return url
		}
		if attempt == 5 {
			t.Fatalf("glen serve found its ports taken %d times", attempt)
		}
	}
}

// serveGlen starts glen serve on two free ports and waits until it
// answers. It reports taken when glen exited because a port was taken.
func serveGlen(t *testing.T, bin string, env []string, adminKey string) (string, bool) {
	admin, sandbox := freePort(t), freePort(t)
	var logs bytes.Buffer
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(env,
		fmt.Sprintf("GLEN_ADMIN_LISTEN=127.0.0.1:%d", admin),
		fmt.Sprintf("GLEN_SANDBOX_LISTEN=127.0.0.1:%d", sandbox),
	)
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	stop := func() {
		_ = cmd.Process.Kill()
		<-exited
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", admin)
	api := glen.NewClient(url, adminKey)
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-exited:
			if strings.Contains(logs.String(), "Address already in use") {
				return url, true
			}
			t.Fatalf("glen serve exited before it answered on %s:\n%s", url, logs.String())
		default:
		}
		// A short probe: whatever else holds the port may never answer.
		probe, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := api.GetAgent(probe, "readiness-probe")
		cancel()
		if glen.IsNotFound(err) {
			t.Cleanup(func() {
				stop()
				if t.Failed() {
					t.Logf("glen serve output:\n%s", logs.String())
				}
			})
			return url, false
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("glen serve did not answer on %s: %v\n%s", url, err, logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func jsonStrings(xs []string) string {
	b, _ := json.Marshal(xs)
	return string(b)
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func randomKey(t *testing.T) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
