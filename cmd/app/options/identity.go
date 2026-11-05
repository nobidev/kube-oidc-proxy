// Copyright Jetstack Ltd. See LICENSE for details.
package options

import (
	"github.com/spf13/pflag"
	cliflag "k8s.io/component-base/cli/flag"

	"github.com/jetstack/kube-oidc-proxy/pkg/proxy/identityfilter"
)

type IdentityFilterOptions struct {
	AllowedUsernameRegex []string
	DeniedUsernameRegex  []string
	AllowedGroupRegex    []string
	DeniedGroupRegex     []string
	RequiredGroups       []string
}

func NewIdentityFilterOptions(nfs *cliflag.NamedFlagSets) *IdentityFilterOptions {
	return new(IdentityFilterOptions).AddFlags(nfs.FlagSet("Identity Filter"))
}

func (i *IdentityFilterOptions) AddFlags(fs *pflag.FlagSet) *IdentityFilterOptions {
	const anchored = " Expressions are anchored and must match the whole value. " +
		"Repeat the flag to add more expressions."

	fs.StringArrayVar(&i.AllowedUsernameRegex, "allowed-username-regex", i.AllowedUsernameRegex, ""+
		"If set, the username (after prefixing) must match at least one of these regular "+
		"expressions, otherwise the request is rejected with 403. Also applied to "+
		"impersonated users (Impersonate-User)."+anchored)

	fs.StringArrayVar(&i.DeniedUsernameRegex, "denied-username-regex", i.DeniedUsernameRegex, ""+
		"Requests whose username (after prefixing) matches any of these regular expressions "+
		"are rejected with 403. Takes precedence over --allowed-username-regex. Also applied "+
		"to impersonated users (Impersonate-User)."+anchored)

	fs.StringArrayVar(&i.AllowedGroupRegex, "allowed-group-regex", i.AllowedGroupRegex, ""+
		"If set, every group of the user (after prefixing) must match at least one of these "+
		"regular expressions, otherwise the request is rejected with 403. Also applied to "+
		"impersonated groups (Impersonate-Group)."+anchored)

	fs.StringArrayVar(&i.DeniedGroupRegex, "denied-group-regex", i.DeniedGroupRegex, ""+
		"Requests where any group of the user (after prefixing) matches one of these regular "+
		"expressions are rejected with 403. Takes precedence over --allowed-group-regex. Also "+
		"applied to impersonated groups (Impersonate-Group)."+anchored)

	fs.StringArrayVar(&i.RequiredGroups, "required-group", i.RequiredGroups, ""+
		"Exact group name (after prefixing) the authenticated OIDC user must be a member of, "+
		"otherwise the request is rejected with 403. Repeat the flag to require several "+
		"groups; the user must be a member of all of them.")

	return i
}

func (i *IdentityFilterOptions) Config() identityfilter.Config {
	return identityfilter.Config{
		AllowedUsernameRegex: i.AllowedUsernameRegex,
		DeniedUsernameRegex:  i.DeniedUsernameRegex,
		AllowedGroupRegex:    i.AllowedGroupRegex,
		DeniedGroupRegex:     i.DeniedGroupRegex,
		RequiredGroups:       i.RequiredGroups,
	}
}

func (i *IdentityFilterOptions) Validate() error {
	_, err := identityfilter.New(i.Config())
	return err
}
