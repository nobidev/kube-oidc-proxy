// Copyright Jetstack Ltd. See LICENSE for details.

// Package identityfilter restricts which users and groups may use the proxy.
//
// Usernames and groups are matched against allow/deny regular expressions.
// Every expression is anchored, i.e. it has to match the whole value
// (`admins` does not match `not-admins`). A value is permitted when it matches
// at least one allow expression (or no allow expression is configured) and
// matches no deny expression. Deny always wins over allow.
//
// Required groups are exact group names the authenticated user has to be a
// member of, all of them (AND).
package identityfilter

import (
	"errors"
	"fmt"
	"regexp"

	"k8s.io/apiserver/pkg/authentication/user"
)

// ErrDenied is returned (wrapped) whenever an identity is rejected.
var ErrDenied = errors.New("identity is not permitted by the proxy identity filter")

// DeniedError describes why an identity was rejected. The reason is meant for
// logs only and must not be returned to the client.
type DeniedError struct {
	Reason string
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrDenied.Error(), e.Reason)
}

func (e *DeniedError) Is(target error) bool {
	return target == ErrDenied
}

// Config holds the raw (uncompiled) filter configuration.
type Config struct {
	AllowedUsernameRegex []string
	DeniedUsernameRegex  []string
	AllowedGroupRegex    []string
	DeniedGroupRegex     []string
	RequiredGroups       []string
}

// Filter is a compiled identity filter. A nil *Filter permits everything.
type Filter struct {
	allowedUsername []*regexp.Regexp
	deniedUsername  []*regexp.Regexp
	allowedGroup    []*regexp.Regexp
	deniedGroup     []*regexp.Regexp
	requiredGroups  []string
}

// New compiles the configuration. It returns a nil Filter when nothing is
// configured.
func New(c Config) (*Filter, error) {
	var (
		f    Filter
		errs []error
	)

	compile := func(flag string, exprs []string) []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, expr := range exprs {
			re, cerr := Compile(expr)
			if cerr != nil {
				errs = append(errs, fmt.Errorf("invalid %s %q: %w", flag, expr, cerr))
				continue
			}
			out = append(out, re)
		}
		return out
	}

	f.allowedUsername = compile("allowed username regex", c.AllowedUsernameRegex)
	f.deniedUsername = compile("denied username regex", c.DeniedUsernameRegex)
	f.allowedGroup = compile("allowed group regex", c.AllowedGroupRegex)
	f.deniedGroup = compile("denied group regex", c.DeniedGroupRegex)

	for _, g := range c.RequiredGroups {
		if g == "" {
			errs = append(errs, errors.New("required group must not be empty"))
			continue
		}
		f.requiredGroups = append(f.requiredGroups, g)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if len(f.allowedUsername)+len(f.deniedUsername)+len(f.allowedGroup)+
		len(f.deniedGroup)+len(f.requiredGroups) == 0 {
		return nil, nil
	}

	return &f, nil
}

// Compile compiles an expression anchored to the whole value.
func Compile(expr string) (*regexp.Regexp, error) {
	if expr == "" {
		return nil, errors.New("expression must not be empty")
	}
	if _, err := regexp.Compile(expr); err != nil {
		return nil, err
	}
	return regexp.Compile(`^(?:` + expr + `)$`)
}

func permitted(value string, allow, deny []*regexp.Regexp) (bool, string) {
	for _, re := range deny {
		if re.MatchString(value) {
			return false, "matches deny expression " + re.String()
		}
	}
	if len(allow) == 0 {
		return true, ""
	}
	for _, re := range allow {
		if re.MatchString(value) {
			return true, ""
		}
	}
	return false, "matches no allow expression"
}

// CheckUsername validates a username against the username allow/deny lists.
func (f *Filter) CheckUsername(name string) error {
	if f == nil {
		return nil
	}
	if ok, why := permitted(name, f.allowedUsername, f.deniedUsername); !ok {
		return &DeniedError{Reason: fmt.Sprintf("username %q %s", name, why)}
	}
	return nil
}

// CheckGroups validates every group against the group allow/deny lists. Any
// single rejected group rejects the whole identity.
func (f *Filter) CheckGroups(groups []string) error {
	if f == nil {
		return nil
	}
	for _, g := range groups {
		if ok, why := permitted(g, f.allowedGroup, f.deniedGroup); !ok {
			return &DeniedError{Reason: fmt.Sprintf("group %q %s", g, why)}
		}
	}
	return nil
}

// CheckRequiredGroups ensures that groups contains every required group.
func (f *Filter) CheckRequiredGroups(groups []string) error {
	if f == nil || len(f.requiredGroups) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		have[g] = struct{}{}
	}
	for _, rg := range f.requiredGroups {
		if _, ok := have[rg]; !ok {
			return &DeniedError{Reason: fmt.Sprintf("missing required group %q", rg)}
		}
	}
	return nil
}

// CheckAuthenticated validates the identity obtained from the OIDC token:
// username, groups and required groups (checked after the group filter, which
// rejects instead of dropping, so the order only matters for the reason).
func (f *Filter) CheckAuthenticated(u user.Info) error {
	if f == nil {
		return nil
	}
	if err := f.CheckUsername(u.GetName()); err != nil {
		return err
	}
	if err := f.CheckGroups(u.GetGroups()); err != nil {
		return err
	}
	return f.CheckRequiredGroups(u.GetGroups())
}

// CheckImpersonationTarget validates the identity requested through
// Impersonate-* headers: username (when set) and groups. Required groups
// apply to the authenticated user only.
func (f *Filter) CheckImpersonationTarget(u user.Info) error {
	if f == nil {
		return nil
	}
	if name := u.GetName(); name != "" {
		if err := f.CheckUsername(name); err != nil {
			return &DeniedError{Reason: "impersonation target " + err.(*DeniedError).Reason}
		}
	}
	if err := f.CheckGroups(u.GetGroups()); err != nil {
		return &DeniedError{Reason: "impersonation target " + err.(*DeniedError).Reason}
	}
	return nil
}
