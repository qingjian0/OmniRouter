package deepseekweb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"omnirouter/internal/provider"
	"omnirouter/internal/websession"
)

type testAuthorizationLoader struct {
	material AuthorizationMaterial
	err      error
}

func (a testAuthorizationLoader) LoadAuthorization(context.Context, string) (AuthorizationMaterial, error) {
	return AuthorizationMaterial{Token: append([]byte(nil), a.material.Token...), CookieHeader: append([]byte(nil), a.material.CookieHeader...)}, a.err
}

type testConversationLease struct {
	lineage   websession.Lineage
	commits   int
	closed    bool
	commitErr error
}

func (l *testConversationLease) Lineage() websession.Lineage { return l.lineage }
func (l *testConversationLease) Commit(_ context.Context, conversationID, messageID string) (websession.Lineage, error) {
	l.commits++
	if l.commitErr != nil {
		return websession.Lineage{}, l.commitErr
	}
	l.lineage.UpstreamConversationID = conversationID
	l.lineage.ParentMessageID = messageID
	return l.lineage, nil
}
func (l *testConversationLease) Close() { l.closed = true }

type testLeaseManager struct {
	lease *testConversationLease
	err   error
}

func (m testLeaseManager) BeginLease(context.Context, string, string, string) (ConversationLease, error) {
	return m.lease, m.err
}

type testCompletionTransport struct {
	result  CompletionResult
	err     error
	updates []PatchUpdate
}

func (t testCompletionTransport) Stream(_ context.Context, _ AuthorizationMaterial, _ ChatCompletionInput, _ websession.Lineage, emit func(PatchUpdate) error) (CompletionResult, error) {
	for _, update := range t.updates {
		if err := emit(update); err != nil {
			return CompletionResult{}, err
		}
	}
	return t.result, t.err
}

func newTestExecutor(t *testing.T, transport UpstreamCompletionTransport, lease *testConversationLease) *ExecutorProvider {
	t.Helper()
	p, err := NewExecutorProvider(ExecutorConfig{
		ID: "deepseek-web", AccountID: "account-1", Models: []string{"deepseek-chat"},
		Authorization: testAuthorizationLoader{material: AuthorizationMaterial{Token: []byte("token"), CookieHeader: []byte("ds_session_id=session")}},
		Leases:        testLeaseManager{lease: lease}, Transport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testRequest() *provider.Request {
	return &provider.Request{Model: "deepseek-chat", ConversationID: "local-conversation-1", Stream: true,
		Input: []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hello"}]}`)}
}

func TestExecutorProviderCommitsOnlyAfterVerifiedTerminal(t *testing.T) {
	lease := &testConversationLease{}
	transport := testCompletionTransport{
		result:  CompletionResult{UpstreamConversationID: "remote-conversation", ResponseMessageID: "response-2", Completed: true},
		updates: []PatchUpdate{{ContentDelta: "answer"}, {Done: true}},
	}
	p := newTestExecutor(t, transport, lease)
	var events []provider.Event
	if err := p.Stream(context.Background(), testRequest(), func(event provider.Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if lease.commits != 1 || lease.lineage.ParentMessageID != "response-2" || !lease.closed {
		t.Fatalf("lineage transaction mismatch: commits=%d lineage=%+v closed=%v", lease.commits, lease.lineage, lease.closed)
	}
	if len(events) != 3 || events[0].Done || events[1].Done || !events[2].Done || !events[2].ExplicitDone {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
	if !strings.Contains(string(events[0].Data), "answer") || !strings.Contains(string(events[1].Data), "finish_reason") {
		t.Fatalf("missing OpenAI stream chunks: %+v", events)
	}
}

func TestExecutorProviderDoesNotCommitWithoutVerifiedTerminal(t *testing.T) {
	lease := &testConversationLease{}
	p := newTestExecutor(t, testCompletionTransport{
		result:  CompletionResult{UpstreamConversationID: "remote", ResponseMessageID: "response", Completed: false},
		updates: []PatchUpdate{{ContentDelta: "partial"}},
	}, lease)
	err := p.Stream(context.Background(), testRequest(), func(provider.Event) error { return nil })
	if !errors.Is(err, ErrIncompleteCompletion) {
		t.Fatalf("got %v, want incomplete completion", err)
	}
	if lease.commits != 0 || !lease.closed {
		t.Fatalf("failed turn changed lineage: commits=%d closed=%v", lease.commits, lease.closed)
	}
}

func TestExecutorProviderDoesNotCommitOnTransportError(t *testing.T) {
	lease := &testConversationLease{}
	upstreamErr := errors.New("upstream disconnected")
	p := newTestExecutor(t, testCompletionTransport{err: upstreamErr}, lease)
	err := p.Stream(context.Background(), testRequest(), func(provider.Event) error { return nil })
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("got %v, want transport error", err)
	}
	if lease.commits != 0 {
		t.Fatalf("transport failure committed lineage %d times", lease.commits)
	}
}

func TestExecutorProviderTreatsMissingConversationIDAsOneTurn(t *testing.T) {
	lease := &testConversationLease{}
	p := newTestExecutor(t, testCompletionTransport{
		result:  CompletionResult{UpstreamConversationID: "remote", ResponseMessageID: "response", Completed: true},
		updates: []PatchUpdate{{ContentDelta: "answer"}, {Done: true}},
	}, lease)
	req := testRequest()
	req.ConversationID = ""
	if err := p.Stream(context.Background(), req, func(provider.Event) error { return nil }); err != nil {
		t.Fatalf("one-turn request without conversation ID failed: %v", err)
	}
	if lease.commits != 0 || !lease.closed {
		t.Fatalf("one-turn request must not persist lineage: commits=%d closed=%v", lease.commits, lease.closed)
	}
}

func TestExecutorProviderGenerateReturnsAggregatedCompletion(t *testing.T) {
	lease := &testConversationLease{}
	p := newTestExecutor(t, testCompletionTransport{
		result:  CompletionResult{UpstreamConversationID: "remote", ResponseMessageID: "response", Completed: true},
		updates: []PatchUpdate{{ContentDelta: "hello "}, {ContentDelta: "world"}, {Done: true}},
	}, lease)
	response, err := p.Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Output), `"content":"hello world"`) {
		t.Fatalf("unexpected aggregated response: %s", response.Output)
	}
	if lease.commits != 1 {
		t.Fatalf("successful non-streaming request committed %d times", lease.commits)
	}
}
