package adapter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/codeswhat/lookout/internal/adapter"
	"github.com/codeswhat/lookout/internal/adapter/drydock"
	"github.com/codeswhat/lookout/internal/generic"
)

type fullAdapterStub struct{}

func (fullAdapterStub) Name() string { return "stub" }

func (fullAdapterStub) Capabilities() []string { return nil }

func (fullAdapterStub) HelloExtension() *adapter.HelloExtension { return nil }

func (fullAdapterStub) OnConnect(context.Context, adapter.MessageSender) error { return nil }

func (fullAdapterStub) RefreshContainers(context.Context) (added, updated, removed []adapter.Container, err error) {
	return nil, nil, nil, nil
}

func (fullAdapterStub) OnContainerRefresh(context.Context, adapter.MessageSender, []adapter.Container, []adapter.Container, []adapter.Container) error {
	return nil
}

func (fullAdapterStub) HandleMessage(context.Context, adapter.MessageSender, string, json.RawMessage) bool {
	return false
}

func (fullAdapterStub) RegisterRoutes(*http.ServeMux, func(http.HandlerFunc) http.Handler) {}

func (fullAdapterStub) PollInterval() int { return 0 }

func TestAdapterInterfaceComposition(t *testing.T) {
	var metadata adapter.MetadataAdapter = fullAdapterStub{}
	var containerSync adapter.ContainerSyncAdapter = fullAdapterStub{}
	var messages adapter.MessageHandlingAdapter = fullAdapterStub{}
	var routes adapter.RouteAdapter = fullAdapterStub{}
	var edge adapter.EdgeAdapter = fullAdapterStub{}
	var server adapter.ServerAdapter = fullAdapterStub{}
	var full adapter.Adapter = fullAdapterStub{}

	_ = metadata
	_ = containerSync
	_ = messages
	_ = routes
	_ = edge
	_ = server
	_ = full

	var genericAdapter adapter.Adapter = &generic.Adapter{}
	var drydockAdapter adapter.Adapter = &drydock.Adapter{}

	_ = genericAdapter
	_ = drydockAdapter
}
