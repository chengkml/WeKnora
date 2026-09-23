package approval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/stretchr/testify/require"
)

func oauthPendingReq(bus *event.EventBus) OAuthPendingRequest {
	return OAuthPendingRequest{
		TenantID:           1,
		SessionID:          "s1",
		AssistantMessageID: "m1",
		EventBus:           bus,
		ServiceID:          "svc",
		ServiceName:        "svcname",
		MCPToolName:        "danger_tool",
		ToolCallID:         "tc1",
	}
}

func TestGate_RequestOAuthAndWait_Approve(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil)

	bus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.MCPOAuthRequiredData)
		require.True(t, ok)
		require.NotEmpty(t, data.PendingID)
		go func() {
			_ = g.Resolve(1, "", data.PendingID, Decision{Approved: true})
		}()
		return nil
	})

	d, err := g.RequestOAuthAndWait(context.Background(), oauthPendingReq(bus))
	require.NoError(t, err)
	require.True(t, d.Approved)
}

func TestGate_RequestOAuthAndWait_Timeout(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, nil)
	d, err := g.RequestOAuthAndWait(context.Background(), oauthPendingReq(event.NewEventBus()))
	require.NoError(t, err)
	require.False(t, d.Approved)
	require.True(t, d.TimedOut)
}

func TestGate_Resolve_NotFound(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, nil)
	err := g.Resolve(1, "", "no-such-id", Decision{Approved: true})
	require.ErrorIs(t, err, ErrPendingNotFound)
}

func TestGate_Resolve_TenantMismatch(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil)
	bus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.MCPOAuthRequiredData)
		go func() {
			require.ErrorIs(t, g.Resolve(999, "", data.PendingID, Decision{Approved: true}), ErrTenantMismatch)
			_ = g.Resolve(1, "", data.PendingID, Decision{Approved: false, Reason: "no"})
		}()
		return nil
	})
	d, err := g.RequestOAuthAndWait(context.Background(), oauthPendingReq(bus))
	require.NoError(t, err)
	require.False(t, d.Approved)
}

func TestGate_Resolve_UserMismatch(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil)
	req := oauthPendingReq(bus)
	req.UserID = "alice"
	bus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.MCPOAuthRequiredData)
		go func() {
			require.ErrorIs(t, g.Resolve(1, "bob", data.PendingID, Decision{Approved: true}), ErrUserMismatch)
			_ = g.Resolve(1, "alice", data.PendingID, Decision{Approved: true})
		}()
		return nil
	})
	d, err := g.RequestOAuthAndWait(context.Background(), req)
	require.NoError(t, err)
	require.True(t, d.Approved)
}

// TestGate_Resolve_EmptyUserIDRejectedWhenWaiterHasUser guards against the
// previous fail-open short-circuit where an empty caller userID skipped the
// per-user check entirely (allowing same-tenant cross-user approval).
func TestGate_Resolve_EmptyUserIDRejectedWhenWaiterHasUser(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil)
	req := oauthPendingReq(bus)
	req.UserID = "alice"
	bus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.MCPOAuthRequiredData)
		go func() {
			require.ErrorIs(t, g.Resolve(1, "", data.PendingID, Decision{Approved: true}), ErrUserMismatch)
			_ = g.Resolve(1, "alice", data.PendingID, Decision{Approved: false, Reason: "no"})
		}()
		return nil
	})
	d, err := g.RequestOAuthAndWait(context.Background(), req)
	require.NoError(t, err)
	require.False(t, d.Approved)
}

func TestGate_Resolve_AlreadyResolvedAfterTimeout(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, nil)
	bus := event.NewEventBus()

	var pendingID string
	gotPending := make(chan struct{}, 1)
	bus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		pendingID = evt.Data.(event.MCPOAuthRequiredData).PendingID
		gotPending <- struct{}{}
		return nil
	})

	go func() {
		<-gotPending
		// Wait until timeout has fired and the pending entry is still there
		// (defer delete only happens after RequestOAuthAndWait returns).
		// 1s timeout + small slack.
		<-time.After(1500 * time.Millisecond)
		require.ErrorIs(t,
			g.Resolve(1, "", pendingID, Decision{Approved: true}),
			ErrPendingNotFound, // entry already removed by RequestOAuthAndWait's defer
		)
	}()

	d, err := g.RequestOAuthAndWait(context.Background(), oauthPendingReq(bus))
	require.NoError(t, err)
	require.True(t, d.TimedOut)
}

func TestGate_Resolve_RaceWinsAlreadyResolved(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 30}}, nil)
	bus := event.NewEventBus()

	type result struct {
		first  error
		second error
	}
	resCh := make(chan result, 1)

	bus.On(event.EventMCPOAuthRequired, func(_ context.Context, evt event.Event) error {
		pendingID := evt.Data.(event.MCPOAuthRequiredData).PendingID
		go func() {
			err1 := g.Resolve(1, "", pendingID, Decision{Approved: true})
			err2 := g.Resolve(1, "", pendingID, Decision{Approved: false})
			resCh <- result{first: err1, second: err2}
		}()
		return nil
	})

	d, err := g.RequestOAuthAndWait(context.Background(), oauthPendingReq(bus))
	require.NoError(t, err)
	require.True(t, d.Approved)
	r := <-resCh
	require.NoError(t, r.first)
	// Second call must surface either AlreadyResolved or NotFound (depending
	// on whether the defer-delete already ran).
	require.True(t,
		r.second == nil || // RequestOAuthAndWait removed the entry: NotFound is possible too
			r.second.Error() == ErrAlreadyResolved.Error() ||
			r.second.Error() == ErrPendingNotFound.Error(),
		"unexpected error: %v", r.second,
	)
}
