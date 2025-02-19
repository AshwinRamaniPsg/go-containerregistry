// Copyright 2018 Google LLC All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kubernetes

import (
	"context"
	"encoding/base64"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclient "k8s.io/client-go/kubernetes/fake"
)

func TestAnonymousFallback(t *testing.T) {
	client := fakeclient.NewSimpleClientset(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "default",
		},
	})

	kc, err := New(context.Background(), client, Options{})
	if err != nil {
		t.Errorf("New() = %v", err)
	}

	reg, err := name.NewRegistry("fake.registry.io", name.WeakValidation)
	if err != nil {
		t.Errorf("NewRegistry() = %v", err)
	}

	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Errorf("Resolve(%v) = %v", reg, err)
	}
	if got, want := auth, authn.Anonymous; got != want {
		t.Errorf("Resolve() = %v, want %v", got, want)
	}
}

func TestAttachedServiceAccount(t *testing.T) {
	username, password := "foo", "bar"
	client := fakeclient.NewSimpleClientset(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svcacct",
			Namespace: "ns",
		},
		ImagePullSecrets: []corev1.LocalObjectReference{{
			Name: "secret",
		}},
	}, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "ns",
		},
		Type: corev1.SecretTypeDockercfg,
		Data: map[string][]byte{
			corev1.DockerConfigKey: []byte(
				fmt.Sprintf(`{"fake.registry.io": {"username": "%s", "password": "%s"}}`,
					username, password),
			),
		},
	})

	kc, err := New(context.Background(), client, Options{
		Namespace:          "ns",
		ServiceAccountName: "svcacct",
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	reg, err := name.NewRegistry("fake.registry.io", name.WeakValidation)
	if err != nil {
		t.Errorf("NewRegistry() = %v", err)
	}

	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Errorf("Resolve(%v) = %v", reg, err)
	}
	got, err := auth.Authorization()
	if err != nil {
		t.Errorf("Authorization() = %v", err)
	}
	want, err := (&authn.Basic{Username: username, Password: password}).Authorization()
	if err != nil {
		t.Errorf("Authorization() = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Resolve() = %v, want %v", got, want)
	}
}

func TestImagePullSecrets(t *testing.T) {
	username, password := "foo", "bar"
	specificUser, specificPass := "very", "specific"
	client := fakeclient.NewSimpleClientset(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "ns",
		},
	}, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "ns",
		},
		Type: corev1.SecretTypeDockercfg,
		Data: map[string][]byte{
			corev1.DockerConfigKey: []byte(
				fmt.Sprintf(`{"fake.registry.io": {"auth": %q}, "fake.registry.io/more/specific": {"auth": %q}}`,
					base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
					base64.StdEncoding.EncodeToString([]byte(specificUser+":"+specificPass))),
			),
		},
	})

	kc, err := New(context.Background(), client, Options{
		Namespace:        "ns",
		ImagePullSecrets: []string{"secret"},
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	repo, err := name.NewRepository("fake.registry.io/more/specific", name.WeakValidation)
	if err != nil {
		t.Errorf("NewRegistry() = %v", err)
	}

	for _, tc := range []struct {
		name   string
		auth   authn.Authenticator
		target authn.Resource
	}{{
		name:   "registry",
		auth:   &authn.Basic{Username: username, Password: password},
		target: repo.Registry,
	}, {
		name:   "repo",
		auth:   &authn.Basic{Username: specificUser, Password: specificPass},
		target: repo,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			auth, err := kc.Resolve(tc.target)
			if err != nil {
				t.Errorf("Resolve(%v) = %v", tc.target, err)
			}
			got, err := auth.Authorization()
			if err != nil {
				t.Errorf("Authorization() = %v", err)
			}
			want, err := tc.auth.Authorization()
			if err != nil {
				t.Errorf("Authorization() = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Resolve() = %v, want %v", got, want)
			}
		})
	}
}

func TestFromPullSecrets(t *testing.T) {
	username, password := "foo", "bar"
	specificUser, specificPass := "very", "specific"

	pullSecrets := []corev1.Secret{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "ns",
		},
		Type: corev1.SecretTypeDockercfg,
		Data: map[string][]byte{
			corev1.DockerConfigKey: []byte(
				fmt.Sprintf(`
					{
						"fake.registry.io": {"auth": %q},
						"fake.registry.io/more/specific": {"auth": %q},
						"http://fake.scheme-registry.io": {"auth": %q},
						"https://fake.scheme-registry.io/more/specific": {"auth": %q},
						"https://index.docker.io/v1/": {"auth": %q}
					}`,
					base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
					base64.StdEncoding.EncodeToString([]byte(specificUser+":"+specificPass)),
					base64.StdEncoding.EncodeToString([]byte(username+":"+password)),
					base64.StdEncoding.EncodeToString([]byte(specificUser+":"+specificPass)),
					base64.StdEncoding.EncodeToString([]byte(username+":"+password))),
			),
		},
	}, {
		// Check that a subsequent Secret that matches the registry is
		// _not_ used; i.e., first matching Secret wins.
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret-2",
			Namespace: "ns",
		},
		Type: corev1.SecretTypeDockercfg,
		Data: map[string][]byte{
			corev1.DockerConfigKey: []byte(
				fmt.Sprintf(`{"fake.registry.io": {"auth": %q}}`,
					base64.StdEncoding.EncodeToString([]byte("anotherUser:anotherPass"))),
			),
		},
	}}

	kc, err := NewFromPullSecrets(context.Background(), pullSecrets)
	if err != nil {
		t.Fatalf("NewFromPullSecrets() = %v", err)
	}

	repo, err := name.NewRepository("fake.registry.io/more/specific", name.WeakValidation)
	if err != nil {
		t.Errorf("NewRegistry() = %v", err)
	}

	schemeRepo, err := name.NewRepository("fake.scheme-registry.io/more/specific", name.WeakValidation)
	if err != nil {
		t.Errorf("NewRegistry() = %v", err)
	}

	dockerHubRepo, err := name.NewRepository("nginx", name.WeakValidation)
	if err != nil {
		t.Errorf("NewRegistry() = %v", err)
	}

	for _, tc := range []struct {
		name   string
		auth   authn.Authenticator
		target authn.Resource
	}{{
		name:   "registry",
		auth:   &authn.Basic{Username: username, Password: password},
		target: repo.Registry,
	}, {
		name:   "repo",
		auth:   &authn.Basic{Username: specificUser, Password: specificPass},
		target: repo,
	}, {
		name:   "registry with scheme",
		auth:   &authn.Basic{Username: username, Password: password},
		target: schemeRepo.Registry,
	}, {
		name:   "repo with scheme",
		auth:   &authn.Basic{Username: specificUser, Password: specificPass},
		target: schemeRepo,
	}, {
		name:   "docker hub repo",
		auth:   &authn.Basic{Username: username, Password: password},
		target: dockerHubRepo,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			auth, err := kc.Resolve(tc.target)
			if err != nil {
				t.Errorf("Resolve(%v) = %v", tc.target, err)
			}
			got, err := auth.Authorization()
			if err != nil {
				t.Errorf("Authorization() = %v", err)
			}
			want, err := tc.auth.Authorization()
			if err != nil {
				t.Errorf("Authorization() = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Resolve() = %v, want %v", got, want)
			}
		})
	}
}
