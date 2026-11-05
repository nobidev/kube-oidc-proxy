// Copyright Jetstack Ltd. See LICENSE for details.
package identityfilter

import (
	"errors"
	"testing"

	"k8s.io/apiserver/pkg/authentication/user"
)

func mustNew(t *testing.T, c Config) *Filter {
	t.Helper()
	f, err := New(c)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	return f
}

func TestNew(t *testing.T) {
	f, err := New(Config{})
	if err != nil || f != nil {
		t.Fatalf("empty config: expected nil filter and no error, got %v, %v", f, err)
	}

	for name, c := range map[string]Config{
		"bad allowed username": {AllowedUsernameRegex: []string{"("}},
		"bad denied username":  {DeniedUsernameRegex: []string{"[a-"}},
		"bad allowed group":    {AllowedGroupRegex: []string{"*"}},
		"bad denied group":     {DeniedGroupRegex: []string{"a)"}},
		"empty expression":     {DeniedGroupRegex: []string{""}},
		"empty required group": {RequiredGroups: []string{""}},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestNilFilterPermitsAll(t *testing.T) {
	var f *Filter
	u := &user.DefaultInfo{Name: "system:admin", Groups: []string{"system:masters"}}
	if err := f.CheckAuthenticated(u); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
	if err := f.CheckImpersonationTarget(u); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestUsername(t *testing.T) {
	f := mustNew(t, Config{
		AllowedUsernameRegex: []string{`oidc:.+@example\.com`, `oidc:svc-.*`},
		DeniedUsernameRegex:  []string{`oidc:svc-legacy`, `system:.*`},
	})

	for name, exp := range map[string]bool{
		"oidc:alice@example.com":      true,
		"oidc:svc-ci":                 true,
		"oidc:svc-legacy":             false, // deny wins over allow
		"oidc:alice@example.com.evil": false, // anchored
		"xoidc:alice@example.com":     false, // anchored
		"oidc:bob@other.com":          false,
		"system:admin":                false,
		"":                            false,
	} {
		err := f.CheckUsername(name)
		if got := err == nil; got != exp {
			t.Errorf("%q: expected permitted=%t, got err=%v", name, exp, err)
		}
		if err != nil && !errors.Is(err, ErrDenied) {
			t.Errorf("%q: expected ErrDenied, got %v", name, err)
		}
	}
}

func TestGroups(t *testing.T) {
	f := mustNew(t, Config{
		AllowedGroupRegex: []string{`oidc:k8s-.*`},
		DeniedGroupRegex:  []string{`oidc:k8s-banned`, `system:masters`},
	})

	for name, tc := range map[string]struct {
		groups []string
		exp    bool
	}{
		"no groups":           {nil, true},
		"all allowed":         {[]string{"oidc:k8s-dev", "oidc:k8s-ops"}, true},
		"one not allowed":     {[]string{"oidc:k8s-dev", "oidc:hr"}, false},
		"one denied":          {[]string{"oidc:k8s-dev", "oidc:k8s-banned"}, false},
		"system:masters":      {[]string{"system:masters"}, false},
		"anchored, substring": {[]string{"x-oidc:k8s-dev"}, false},
	} {
		err := f.CheckGroups(tc.groups)
		if got := err == nil; got != tc.exp {
			t.Errorf("%s: expected permitted=%t, got err=%v", name, tc.exp, err)
		}
	}
}

func TestDenyOnlyGroups(t *testing.T) {
	f := mustNew(t, Config{DeniedGroupRegex: []string{`system:.*`}})
	if err := f.CheckGroups([]string{"anything", "dev"}); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
	if err := f.CheckGroups([]string{"dev", "system:masters"}); err == nil {
		t.Error("expected system:masters to be denied")
	}
}

func TestRequiredGroups(t *testing.T) {
	f := mustNew(t, Config{RequiredGroups: []string{"k8s-users", "mfa"}})

	for name, tc := range map[string]struct {
		groups []string
		exp    bool
	}{
		"none":        {nil, false},
		"only one":    {[]string{"k8s-users"}, false},
		"all (AND)":   {[]string{"mfa", "other", "k8s-users"}, true},
		"exact match": {[]string{"k8s-users-x", "mfa"}, false},
	} {
		err := f.CheckRequiredGroups(tc.groups)
		if got := err == nil; got != tc.exp {
			t.Errorf("%s: expected permitted=%t, got err=%v", name, tc.exp, err)
		}
	}
}

func TestCheckAuthenticated(t *testing.T) {
	f := mustNew(t, Config{
		DeniedGroupRegex: []string{`banned`},
		RequiredGroups:   []string{"k8s"},
	})
	// group filter rejects (does not drop), even if required groups are present
	if err := f.CheckAuthenticated(&user.DefaultInfo{Name: "a", Groups: []string{"k8s", "banned"}}); err == nil {
		t.Error("expected rejection because of denied group")
	}
	if err := f.CheckAuthenticated(&user.DefaultInfo{Name: "a", Groups: []string{"k8s"}}); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCheckImpersonationTarget(t *testing.T) {
	f := mustNew(t, Config{
		DeniedUsernameRegex: []string{`system:admin`},
		DeniedGroupRegex:    []string{`system:masters`},
		RequiredGroups:      []string{"k8s"},
	})
	// required groups are not enforced on the target
	if err := f.CheckImpersonationTarget(&user.DefaultInfo{Name: "bob"}); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
	if err := f.CheckImpersonationTarget(&user.DefaultInfo{Name: "system:admin"}); !errors.Is(err, ErrDenied) {
		t.Errorf("expected ErrDenied, got %v", err)
	}
	if err := f.CheckImpersonationTarget(&user.DefaultInfo{Name: "bob", Groups: []string{"system:masters"}}); !errors.Is(err, ErrDenied) {
		t.Errorf("expected ErrDenied, got %v", err)
	}
}
