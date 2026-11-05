// Copyright Jetstack Ltd. See LICENSE for details.

// Package autogroups resolves the groups of a user from OpenShift Group
// objects (user.openshift.io/v1, Group.users) instead of the OIDC token.
//
// All Group objects are watched into an in-memory cache indexed by user, so a
// lookup never hits the API server. The resolver is fail-closed: Start returns
// an error when the OpenShift Group API is not available or the cache cannot
// be synced in time, and the proxy refuses to start.
package autogroups

import (
	"context"
	"fmt"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const usersIndex = "users"

// GroupVersionResource of OpenShift groups.
var GroupVersionResource = schema.GroupVersionResource{
	Group:    "user.openshift.io",
	Version:  "v1",
	Resource: "groups",
}

// Resolver returns the OpenShift groups a user is a member of.
type Resolver struct {
	informer cache.SharedIndexInformer
}

// New creates a resolver. Call Start before using it.
func New(client dynamic.Interface, resync time.Duration) *Resolver {
	informer := dynamicinformer.NewFilteredDynamicInformer(
		client, GroupVersionResource, "", resync,
		cache.Indexers{usersIndex: usersIndexFunc}, nil,
	).Informer()

	return &Resolver{informer: informer}
}

// CheckAPIAvailable returns an error if the cluster does not serve
// user.openshift.io/v1 groups (i.e. it is not OpenShift).
func CheckAPIAvailable(d discovery.DiscoveryInterface) error {
	gv := GroupVersionResource.GroupVersion().String()
	list, err := d.ServerResourcesForGroupVersion(gv)
	if err != nil {
		return fmt.Errorf("--auto-groups requires the OpenShift %s API: %w", gv, err)
	}
	for _, r := range list.APIResources {
		if r.Name == GroupVersionResource.Resource {
			return nil
		}
	}
	return fmt.Errorf("--auto-groups requires the OpenShift %s API: resource %q not found",
		gv, GroupVersionResource.Resource)
}

// Start runs the watch and blocks until the cache is synced, the timeout
// expires or stopCh is closed.
func (r *Resolver) Start(stopCh <-chan struct{}, timeout time.Duration) error {
	go r.informer.Run(stopCh)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	if !cache.WaitForCacheSync(ctx.Done(), r.informer.HasSynced) {
		return fmt.Errorf("timed out after %s waiting for the OpenShift group cache to sync "+
			"(does the proxy service account have list/watch on groups.user.openshift.io?)", timeout)
	}

	klog.Infof("OpenShift group cache synced (%d groups)", len(r.informer.GetStore().ListKeys()))
	return nil
}

// HasSynced reports whether the initial list has been loaded.
func (r *Resolver) HasSynced() bool {
	return r.informer.HasSynced()
}

// GroupsFor returns the sorted names of all groups that list username in
// Group.users. The match is exact.
func (r *Resolver) GroupsFor(username string) []string {
	if username == "" {
		return nil
	}

	objs, err := r.informer.GetIndexer().ByIndex(usersIndex, username)
	if err != nil {
		klog.Errorf("failed to look up OpenShift groups for %q: %s", username, err)
		return nil
	}

	groups := make([]string, 0, len(objs))
	for _, obj := range objs {
		if u, ok := obj.(*unstructured.Unstructured); ok && u.GetName() != "" {
			groups = append(groups, u.GetName())
		}
	}
	sort.Strings(groups)
	return groups
}

// usersIndexFunc indexes a Group by every user in Group.users. It never
// returns an error: an error would make the informer panic, a malformed
// object is simply not indexed.
func usersIndexFunc(obj interface{}) ([]string, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, nil
	}

	users, found, err := unstructured.NestedStringSlice(u.Object, "users")
	if err != nil {
		klog.Warningf("ignoring malformed users of OpenShift group %q: %s", u.GetName(), err)
		return nil, nil
	}
	if !found {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(users))
	out := make([]string, 0, len(users))
	for _, user := range users {
		if user == "" {
			continue
		}
		if _, dup := seen[user]; dup {
			continue
		}
		seen[user] = struct{}{}
		out = append(out, user)
	}
	return out, nil
}
