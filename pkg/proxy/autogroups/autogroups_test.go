// Copyright Jetstack Ltd. See LICENSE for details.
package autogroups

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func group(name string, users ...interface{}) *unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "user.openshift.io/v1",
		"kind":       "Group",
		"metadata":   map[string]interface{}{"name": name},
	}
	if users != nil {
		obj["users"] = users
	}
	return &unstructured.Unstructured{Object: obj}
}

func newFakeClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{GroupVersionResource: "GroupList"}, objs...)
}

func startResolver(t *testing.T, objs ...runtime.Object) (*Resolver, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	client := newFakeClient(objs...)
	r := New(client, 0)
	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })
	if err := r.Start(stopCh, 10*time.Second); err != nil {
		t.Fatalf("start: %s", err)
	}
	if !r.HasSynced() {
		t.Fatal("expected cache to be synced")
	}
	return r, client
}

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestGroupsFor(t *testing.T) {
	r, _ := startResolver(t,
		group("k8s-admins", "alice", "bob"),
		group("k8s-users", "alice", "carol", "alice"), // duplicate user
		group("empty"), // users: null
		group("prefixed", "oidc:alice"),
		group("malformed", "alice", int64(1)), // not indexed, must not panic
	)

	for user, exp := range map[string][]string{
		"alice":      {"k8s-admins", "k8s-users"}, // sorted, deduplicated
		"bob":        {"k8s-admins"},
		"carol":      {"k8s-users"},
		"oidc:alice": {"prefixed"}, // exact match on the final username
		"Alice":      {},           // case sensitive
		"dave":       {},
		"":           nil,
	} {
		got := r.GroupsFor(user)
		if len(got) == 0 && len(exp) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, exp) {
			t.Errorf("%q: expected %v, got %v", user, exp, got)
		}
	}
}

func TestGroupsForFollowsChanges(t *testing.T) {
	r, client := startResolver(t, group("k8s-users", "alice"))
	groups := client.Resource(GroupVersionResource)
	ctx := context.Background()

	// add a group
	if _, err := groups.Create(ctx, group("k8s-admins", "alice"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return reflect.DeepEqual(r.GroupsFor("alice"), []string{"k8s-admins", "k8s-users"}) })

	// remove alice from a group
	if _, err := groups.Update(ctx, group("k8s-admins", "bob"), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		return reflect.DeepEqual(r.GroupsFor("alice"), []string{"k8s-users"}) &&
			reflect.DeepEqual(r.GroupsFor("bob"), []string{"k8s-admins"})
	})

	// delete a group
	if err := groups.Delete(ctx, "k8s-users", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(r.GroupsFor("alice")) == 0 })
}

func TestStartTimeout(t *testing.T) {
	client := newFakeClient()
	// make every list fail so the cache never syncs (e.g. missing RBAC)
	client.PrependReactor("list", "groups", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})
	stopCh := make(chan struct{})
	defer close(stopCh)

	err := New(client, 0).Start(stopCh, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestCheckAPIAvailable(t *testing.T) {
	d := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	if err := CheckAPIAvailable(d); err == nil {
		t.Error("expected error when user.openshift.io/v1 is not served")
	}

	d.Resources = []*metav1.APIResourceList{{
		GroupVersion: "user.openshift.io/v1",
		APIResources: []metav1.APIResource{{Name: "users"}},
	}}
	if err := CheckAPIAvailable(d); err == nil {
		t.Error("expected error when groups resource is missing")
	}

	d.Resources[0].APIResources = append(d.Resources[0].APIResources, metav1.APIResource{Name: "groups"})
	if err := CheckAPIAvailable(d); err != nil {
		t.Errorf("unexpected error: %s", err)
	}
}
