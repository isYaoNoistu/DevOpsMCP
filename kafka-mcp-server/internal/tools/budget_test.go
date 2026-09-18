package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"kafka-mcp-server/internal/targets"
)

type blockingBackend struct {
	Backend
	entered chan struct{}
	closed  chan struct{}
}

func (b *blockingBackend) Capabilities(ctx context.Context) (any, error) {
	b.entered <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (b *blockingBackend) Close() { b.closed <- struct{}{} }
func fixtureRegistry(t *testing.T) *targets.Registry {
	t.Helper()
	p := filepath.Join(t.TempDir(), "targets.json")
	err := os.WriteFile(p, []byte(`{"targets":[{"name":"test","brokers":["localhost:9092"],"topics":["orders-*"],"groups":["worker-*"]},{"name":"other","brokers":["localhost:9092"],"topics":["orders-*"],"groups":["worker-*"]}]}`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	return targets.New(p)
}
func responseStatus(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &v); err != nil {
		t.Fatal(err)
	}
	return v["status"].(string)
}
func TestPerTargetBudgetAndCancellation(t *testing.T) {
	entered, closed := make(chan struct{}, 3), make(chan struct{}, 3)
	d := Deps{Reg: fixtureRegistry(t), Budget: NewBudget(2), Factory: func(targets.Target) (Backend, error) { return &blockingBackend{entered: entered, closed: closed}, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 3)
	for i := 0; i < 2; i++ {
		go func() { d.Capabilities(ctx, nil, TargetInput{Target: "test"}); done <- struct{}{} }()
		<-entered
	}
	r, _, _ := d.Capabilities(ctx, nil, TargetInput{Target: "test"})
	if responseStatus(t, r) != "busy" {
		t.Fatal("third call not rejected")
	}
	go func() { d.Capabilities(ctx, nil, TargetInput{Target: "other"}); done <- struct{}{} }()
	<-entered
	cancel()
	for i := 0; i < 3; i++ {
		<-done
		<-closed
	}
	if !d.Budget.acquire("test") {
		t.Fatal("permit leaked on cancel")
	}
	d.Budget.release("test")
}
func TestBudgetReleasedOnFactoryFailure(t *testing.T) {
	d := Deps{Reg: fixtureRegistry(t), Budget: NewBudget(1), Factory: func(targets.Target) (Backend, error) { return nil, errors.New("private credential") }}
	for i := 0; i < 3; i++ {
		r, _, _ := d.Capabilities(context.Background(), nil, TargetInput{Target: "test"})
		if responseStatus(t, r) != "client_configuration_error" {
			t.Fatal("factory failure leaked permit")
		}
	}
}
func TestCanceledCallDoesNotDial(t *testing.T) {
	d := Deps{Reg: fixtureRegistry(t), Factory: func(targets.Target) (Backend, error) { t.Fatal("cancelled call dialed"); return nil, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _, _ := d.Capabilities(ctx, nil, TargetInput{Target: "test"})
	if responseStatus(t, r) != "canceled" {
		t.Fatal("wrong cancellation status")
	}
}
func TestExplicitGroupTopicsGateBeforeDial(t *testing.T) {
	d := Deps{Reg: fixtureRegistry(t), Factory: func(targets.Target) (Backend, error) { t.Fatal("forbidden scope dialed"); return nil, nil }}
	for _, topics := range [][]string{{"private"}, {"orders-*"}, {"../"}, make([]string, 21)} {
		r, _, _ := d.Group(context.Background(), nil, GroupInput{Target: "test", Group: "worker-a", Topics: topics})
		if !r.IsError {
			t.Fatal("invalid scope accepted")
		}
	}
}
