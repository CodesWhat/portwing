package generic

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/codeswhat/lookout/internal/adapter"
)

// Adapter is a no-op adapter for standalone Lookout use without any
// controller integration. It implements the adapter.Adapter interface
// with empty/nil returns.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string              { return "generic" }
func (a *Adapter) Capabilities() []string     { return nil }
func (a *Adapter) HelloExtension() *adapter.HelloExtension { return nil }
func (a *Adapter) PollInterval() int          { return 0 }

func (a *Adapter) OnConnect(_ context.Context, _ adapter.MessageSender) error {
	return nil
}

func (a *Adapter) RefreshContainers(_ context.Context) (added, updated, removed []adapter.Container, err error) {
	return nil, nil, nil, nil
}

func (a *Adapter) OnContainerRefresh(_ context.Context, _ adapter.MessageSender, _, _, _ []adapter.Container) error {
	return nil
}

func (a *Adapter) HandleMessage(_ context.Context, _ adapter.MessageSender, _ string, _ json.RawMessage) bool {
	return false
}

func (a *Adapter) RegisterRoutes(_ *http.ServeMux, _ func(http.HandlerFunc) http.Handler) {}
