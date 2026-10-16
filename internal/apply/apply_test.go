package apply

import (
	"encoding/json"
	"testing"

	"github.com/denis/docker-fs-viewer/internal/model"
)

func TestReplacementConfigUsesNewImageAndSafeNetworkFields(t *testing.T) {
	raw := json.RawMessage(`{
		"Config":{"Image":"old:latest","Env":["A=B"],"Cmd":["echo","ok"]},
		"HostConfig":{"NetworkMode":"demo","Binds":["volume:/data:rw"]}
	}`)
	inspect := &model.Inspect{ID: "1234567890123456", Name: "/demo"}
	inspect.Mounts = []model.Mount{
		{Type: "volume", Name: "named", Destination: "/data", RW: true},
		{Type: "volume", Name: "anonymous-id", Destination: "/cache", RW: false},
	}
	inspect.NetworkSettings.Networks = map[string]model.EndpointSettings{
		"demo": {
			Aliases:    []string{"1234567890123456", "123456789012", "demo", "custom"},
			IPAddress:  "172.20.0.2",
			MacAddress: "02:42:ac:14:00:02",
		},
	}
	body, err := replacementConfig(raw, inspect, "replacement:latest")
	if err != nil {
		t.Fatal(err)
	}
	if body["Image"] != "replacement:latest" {
		t.Fatalf("image not replaced: %#v", body["Image"])
	}
	host := body["HostConfig"].(map[string]any)
	if host["NetworkMode"] != "demo" {
		t.Fatalf("host config not retained: %#v", host)
	}
	binds := host["Binds"].([]any)
	if len(binds) != 2 || binds[1] != "anonymous-id:/cache:ro" {
		t.Fatalf("anonymous volume was not reattached: %#v", binds)
	}
	networking := body["NetworkingConfig"].(map[string]any)
	endpoints := networking["EndpointsConfig"].(map[string]any)
	endpoint := endpoints["demo"].(map[string]any)
	aliases := endpoint["Aliases"].([]string)
	if len(aliases) != 1 || aliases[0] != "custom" {
		t.Fatalf("runtime aliases were not filtered: %#v", aliases)
	}
	if _, exists := endpoint["IPAddress"]; exists {
		t.Fatal("runtime IP address must not be copied")
	}
}

func TestValidateRejectsContainerNetworkNamespace(t *testing.T) {
	inspect := &model.Inspect{HostConfig: json.RawMessage(`{"NetworkMode":"container:abc"}`)}
	if err := validateConfig(inspect); err == nil {
		t.Fatal("expected container network namespace to be rejected")
	}
}
