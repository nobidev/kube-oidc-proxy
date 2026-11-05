// Copyright Jetstack Ltd. See LICENSE for details.
package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"

	azv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/request/bearertoken"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/server"

	"github.com/jetstack/kube-oidc-proxy/cmd/app/options"
	"github.com/jetstack/kube-oidc-proxy/pkg/proxy/audit"
	"github.com/jetstack/kube-oidc-proxy/pkg/proxy/hooks"
	"github.com/jetstack/kube-oidc-proxy/pkg/proxy/identityfilter"
	"github.com/jetstack/kube-oidc-proxy/pkg/proxy/subjectaccessreview"
)

// allowAllSAR allows every impersonation, so that only the identity filter
// decides.
type allowAllSAR struct{}

func (allowAllSAR) Create(_ context.Context, r *azv1.SubjectAccessReview, _ metav1.CreateOptions) (*azv1.SubjectAccessReview, error) {
	r.Status.Allowed = true
	return r, nil
}

type recordingRT struct {
	mu   sync.Mutex
	reqs []http.Header
}

func (r *recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req.Header.Clone())
	r.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
}

// token format: "<username>|<group1>,<group2>"
var identityFilterTokens = authenticator.TokenFunc(func(_ context.Context, tok string) (*authenticator.Response, bool, error) {
	parts := strings.SplitN(tok, "|", 2)
	u := &user.DefaultInfo{Name: parts[0]}
	if len(parts) == 2 && parts[1] != "" {
		u.Groups = strings.Split(parts[1], ",")
	}
	return &authenticator.Response{User: u}, true, nil
})

func newIdentityFilterServer(t *testing.T, f *identityfilter.Filter, disableImpersonation bool) (*httptest.Server, *recordingRT) {
	t.Helper()
	sr, _ := subjectaccessreview.New(allowAllSAR{})
	rt := &recordingRT{}
	p := &Proxy{
		oidcRequestAuther:     bearertoken.New(identityFilterTokens),
		subjectAccessReviewer: sr,
		clientTransport:       rt,
		noAuthClientTransport: rt,
		config:                &Config{IdentityFilter: f, DisableImpersonation: disableImpersonation},
		hooks:                 hooks.New(),
	}
	aud, err := audit.New(new(options.AuditOptions), "0.0.0.0:1234", new(server.SecureServingInfo))
	if err != nil {
		t.Fatal(err)
	}
	p.auditor = aud
	p.handleError = p.newErrorHandler()

	u, _ := url.Parse("https://apiserver.invalid")
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.Transport = p
	rp.ErrorHandler = p.handleError

	srv := httptest.NewServer(p.withHandlers(rp))
	t.Cleanup(srv.Close)
	return srv, rt
}

func TestIdentityFilterHandlers(t *testing.T) {
	f, err := identityfilter.New(identityfilter.Config{
		AllowedUsernameRegex: []string{`oidc:.*`},
		DeniedUsernameRegex:  []string{`oidc:mallory`},
		AllowedGroupRegex:    []string{`oidc:.*`},
		DeniedGroupRegex:     []string{`oidc:banned`},
		RequiredGroups:       []string{"oidc:k8s", "oidc:mfa"},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		token   string
		headers map[string][]string
		expCode int
		expUser string
	}{
		"permitted user": {
			token: "oidc:alice|oidc:k8s,oidc:mfa", expCode: 200, expUser: "oidc:alice",
		},
		"username not allowed": {
			token: "system:admin|oidc:k8s,oidc:mfa", expCode: 403,
		},
		"username denied": {
			token: "oidc:mallory|oidc:k8s,oidc:mfa", expCode: 403,
		},
		"group not allowed": {
			token: "oidc:alice|oidc:k8s,oidc:mfa,system:masters", expCode: 403,
		},
		"group denied": {
			token: "oidc:alice|oidc:k8s,oidc:mfa,oidc:banned", expCode: 403,
		},
		"missing one required group (AND)": {
			token: "oidc:alice|oidc:k8s", expCode: 403,
		},
		"no groups": {
			token: "oidc:alice", expCode: 403,
		},
		"impersonate permitted user": {
			token:   "oidc:alice|oidc:k8s,oidc:mfa",
			headers: map[string][]string{"Impersonate-User": {"oidc:bob"}},
			expCode: 200, expUser: "oidc:bob",
		},
		"impersonate user outside allow list": {
			token:   "oidc:alice|oidc:k8s,oidc:mfa",
			headers: map[string][]string{"Impersonate-User": {"system:admin"}},
			expCode: 403,
		},
		"impersonate denied group": {
			token:   "oidc:alice|oidc:k8s,oidc:mfa",
			headers: map[string][]string{"Impersonate-User": {"oidc:bob"}, "Impersonate-Group": {"system:masters"}},
			expCode: 403,
		},
		"impersonated target does not need required groups": {
			token:   "oidc:alice|oidc:k8s,oidc:mfa",
			headers: map[string][]string{"Impersonate-User": {"oidc:bob"}, "Impersonate-Group": {"oidc:dev"}},
			expCode: 200, expUser: "oidc:bob",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv, rt := newIdentityFilterServer(t, f, false)

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/namespaces", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			for k, vs := range tc.headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != tc.expCode {
				t.Fatalf("expected status %d, got %d (%s)", tc.expCode, resp.StatusCode, body)
			}

			if tc.expCode != 200 {
				if len(rt.reqs) != 0 {
					t.Errorf("rejected request must not reach the API server, got %d", len(rt.reqs))
				}
				if strings.Contains(string(body), "oidc:") || strings.Contains(string(body), "regex") {
					t.Errorf("response must not leak filter details: %q", body)
				}
				return
			}

			if len(rt.reqs) != 1 {
				t.Fatalf("expected 1 upstream request, got %d", len(rt.reqs))
			}
			if got := rt.reqs[0].Get("Impersonate-User"); got != tc.expUser {
				t.Errorf("expected Impersonate-User %q, got %q", tc.expUser, got)
			}
		})
	}
}

func TestIdentityFilterDisableImpersonation(t *testing.T) {
	f, err := identityfilter.New(identityfilter.Config{RequiredGroups: []string{"k8s"}})
	if err != nil {
		t.Fatal(err)
	}
	srv, rt := newIdentityFilterServer(t, f, true)

	for token, exp := range map[string]int{"alice|k8s": 200, "bob|dev": 403} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != exp {
			t.Errorf("%s: expected %d, got %d", token, exp, resp.StatusCode)
		}
	}
	if len(rt.reqs) != 1 {
		t.Errorf("expected only the permitted request upstream, got %d", len(rt.reqs))
	}
}
