package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memorySourceSagaStore struct {
	values map[string]sourceSagaCheckpoint
}

func newMemorySourceSagaStore() *memorySourceSagaStore {
	return &memorySourceSagaStore{values: map[string]sourceSagaCheckpoint{}}
}

func (s *memorySourceSagaStore) Load(_ context.Context, requestID string) (sourceSagaCheckpoint, error) {
	return s.values[requestID], nil
}

func (s *memorySourceSagaStore) Save(_ context.Context, checkpoint sourceSagaCheckpoint) error {
	s.values[checkpoint.RequestID] = checkpoint
	return nil
}

type fakeSourceLifecycleClient struct {
	calls        []string
	appFailures  []error
	authFailures []error
}

func (c *fakeSourceLifecycleClient) callRaw(_ context.Context, event string, _ any, _ any) error {
	c.calls = append(c.calls, "app:"+event)
	if len(c.appFailures) == 0 {
		return nil
	}
	err := c.appFailures[0]
	c.appFailures = c.appFailures[1:]
	return err
}

func (c *fakeSourceLifecycleClient) callProviderRaw(
	_ context.Context,
	_ string,
	provider string,
	_ any,
	_ any,
) error {
	c.calls = append(c.calls, "provider:"+provider)
	if len(c.authFailures) == 0 {
		return nil
	}
	err := c.authFailures[0]
	c.authFailures = c.authFailures[1:]
	return err
}

func testSourceCreateRequest() sourceAdminCreateRequest {
	ttl := int64(60)
	return sourceAdminCreateRequest{
		Version: sourceLifecycleContractVersion, RequestID: "source-create-1",
		Source: bridgeMonitorSource{
			ID: "vendor", Name: "Vendor", Weight: 1, Kind: "push",
			Trusted: true, DirectTargets: true, DefaultTTL: &ttl,
		},
		CredentialID: "credential-1", Token: "private-test-token",
		ActorID: "admin-1", CreatedAt: time.Date(2026, 7, 26, 2, 0, 0, 0, time.UTC),
	}
}

func TestSourceCreateSagaResumesWithoutRepeatingCredentialStep(t *testing.T) {
	client := &fakeSourceLifecycleClient{appFailures: []error{errors.New("temporary owner failure"), nil}}
	store := newMemorySourceSagaStore()
	service, err := newSourceLifecycleService(client, store)
	if err != nil {
		t.Fatal(err)
	}
	request := testSourceCreateRequest()
	if _, err := service.Create(context.Background(), request); err == nil {
		t.Fatal("expected transient monitor failure")
	}
	checkpoint := store.values[request.RequestID]
	if !checkpoint.AuthDone || checkpoint.MonitorDone || checkpoint.Fingerprint == "" {
		t.Fatalf("checkpoint after interruption = %#v", checkpoint)
	}
	result, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || !result.Deduped {
		t.Fatalf("resumed result = %#v", result)
	}
	want := []string{
		"provider:" + providerAuthSourceAdminImport,
		"app:" + eventMonitorAdminCommand,
		"app:" + eventMonitorAdminCommand,
	}
	if strings.Join(client.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %#v, want %#v", client.calls, want)
	}
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != len(want) {
		t.Fatal("completed saga repeated an owner operation")
	}
}

func TestSourceCreateSagaCompensatesPermanentMonitorFailure(t *testing.T) {
	client := &fakeSourceLifecycleClient{appFailures: []error{errors.New(`source "vendor" already exists`)}}
	store := newMemorySourceSagaStore()
	service, err := newSourceLifecycleService(client, store)
	if err != nil {
		t.Fatal(err)
	}
	request := testSourceCreateRequest()
	_, err = service.Create(context.Background(), request)
	var domain *bridgeDomainError
	if !errors.As(err, &domain) || domain.Status != 409 || domain.Code != "conflict" {
		t.Fatalf("create error = %#v", err)
	}
	checkpoint := store.values[request.RequestID]
	if !checkpoint.AuthDone || !checkpoint.Compensated || checkpoint.MonitorDone {
		t.Fatalf("compensated checkpoint = %#v", checkpoint)
	}
	want := []string{
		"provider:" + providerAuthSourceAdminImport,
		"app:" + eventMonitorAdminCommand,
		"provider:" + providerAuthSourceAdminRevoke,
	}
	if strings.Join(client.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %#v, want %#v", client.calls, want)
	}
	_, err = service.Create(context.Background(), request)
	if !errors.As(err, &domain) || len(client.calls) != len(want) {
		t.Fatal("compensated saga did not replay its durable failure")
	}
}

func TestSourceSagaRejectsChangedPayloadForSameRequestID(t *testing.T) {
	for _, mutate := range []func(*sourceAdminCreateRequest){
		func(request *sourceAdminCreateRequest) { request.Token = "changed-private-token" },
		func(request *sourceAdminCreateRequest) { request.Source.Name = "Changed" },
		func(request *sourceAdminCreateRequest) { request.Source.Weight = 7 },
		func(request *sourceAdminCreateRequest) { request.Source.Kind = "probe" },
		func(request *sourceAdminCreateRequest) { request.Source.DirectTargets = false },
		func(request *sourceAdminCreateRequest) {
			ttl := int64(120)
			request.Source.DefaultTTL = &ttl
		},
	} {
		client := &fakeSourceLifecycleClient{}
		store := newMemorySourceSagaStore()
		service, _ := newSourceLifecycleService(client, store)
		original := testSourceCreateRequest()
		if _, err := service.Create(context.Background(), original); err != nil {
			t.Fatal(err)
		}
		changed := testSourceCreateRequest()
		mutate(&changed)
		before := len(client.calls)
		if _, err := service.Create(context.Background(), changed); err == nil {
			t.Fatal("changed payload reused a bound request ID")
		}
		if len(client.calls) != before {
			t.Fatal("changed payload reached an owner before fencing")
		}
	}
}

func TestSourceRevokeSagaResumesCredentialRevocation(t *testing.T) {
	client := &fakeSourceLifecycleClient{authFailures: []error{errors.New("temporary auth failure"), nil}}
	store := newMemorySourceSagaStore()
	service, _ := newSourceLifecycleService(client, store)
	request := sourceAdminRevokeRequest{
		Version: sourceLifecycleContractVersion, RequestID: "source-revoke-1",
		SourceID: "vendor", CredentialID: "credential-1", ActorID: "admin-1",
		RevokedAt: time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC),
	}
	if _, err := service.Revoke(context.Background(), request); err == nil {
		t.Fatal("expected credential revoke failure")
	}
	checkpoint := store.values[request.RequestID]
	if !checkpoint.MonitorDone || checkpoint.AuthDone {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
	result, err := service.Revoke(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || !result.Deduped {
		t.Fatalf("result = %#v", result)
	}
	want := []string{
		"app:" + eventMonitorAdminCommand,
		"provider:" + providerAuthSourceAdminRevoke,
		"provider:" + providerAuthSourceAdminRevoke,
	}
	if strings.Join(client.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %#v, want %#v", client.calls, want)
	}
}
