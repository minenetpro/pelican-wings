package remote

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustOutputLineMatcher(t *testing.T, value string) *OutputLineMatcher {
	t.Helper()

	var matcher OutputLineMatcher
	err := json.Unmarshal([]byte(strconv.Quote(value)), &matcher)
	require.NoError(t, err)

	return &matcher
}

func mustProcessConfiguration(t *testing.T, raw string) *ProcessConfiguration {
	t.Helper()

	var configuration ProcessConfiguration
	err := json.Unmarshal([]byte(raw), &configuration)
	require.NoError(t, err)

	return &configuration
}

func TestOutputLineMatcherMarshalJSON(t *testing.T) {
	matcher := mustOutputLineMatcher(t, "regex:^Server marked as running$")

	data, err := json.Marshal(matcher)
	require.NoError(t, err)

	assert.Equal(t, `"regex:^Server marked as running$"`, string(data))

	var decoded OutputLineMatcher
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, matcher.String(), decoded.String())
	assert.NotNil(t, decoded.reg)
}

func TestLocalClientPersistsStartupDoneMatchersAsStrings(t *testing.T) {
	root := t.TempDir()

	client, err := NewLocal(root)
	require.NoError(t, err)

	store := client.(*localClient)
	definition := LocalServerDefinition{
		UUID: "server-1",
		Configuration: ServerConfigurationResponse{
			Settings: json.RawMessage(`{"foo":"bar"}`),
			ProcessConfiguration: &ProcessConfiguration{
				Stop: ProcessStopConfiguration{
					Type:  "command",
					Value: "stop",
				},
			},
		},
	}
	definition.Configuration.ProcessConfiguration.Startup.Done = []*OutputLineMatcher{
		mustOutputLineMatcher(t, "Done loading"),
		mustOutputLineMatcher(t, "regex:^Ready$"),
	}

	err = store.UpsertServer(context.Background(), definition)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(root, localStoreFilename))
	require.NoError(t, err)

	var persisted struct {
		Servers map[string]struct {
			Configuration struct {
				ProcessConfiguration struct {
					Startup struct {
						Done []string `json:"done"`
					} `json:"startup"`
				} `json:"process_configuration"`
			} `json:"configuration"`
		} `json:"servers"`
	}
	err = json.Unmarshal(data, &persisted)
	require.NoError(t, err)

	assert.Equal(t, []string{"Done loading", "regex:^Ready$"}, persisted.Servers["server-1"].Configuration.ProcessConfiguration.Startup.Done)

	reloadedClient, err := NewLocal(root)
	require.NoError(t, err)

	reloaded, err := reloadedClient.(*localClient).GetLocalServer(context.Background(), "server-1")
	require.NoError(t, err)

	require.Len(t, reloaded.Configuration.ProcessConfiguration.Startup.Done, 2)
	assert.Equal(t, "Done loading", reloaded.Configuration.ProcessConfiguration.Startup.Done[0].String())
	assert.Equal(t, "regex:^Ready$", reloaded.Configuration.ProcessConfiguration.Startup.Done[1].String())
	assert.NotNil(t, reloaded.Configuration.ProcessConfiguration.Startup.Done[1].reg)
}

func TestLocalClientPersistsConfigReplacementValues(t *testing.T) {
	root := t.TempDir()

	client, err := NewLocal(root)
	require.NoError(t, err)

	store := client.(*localClient)
	definition := LocalServerDefinition{
		UUID: "server-1",
		Configuration: ServerConfigurationResponse{
			Settings: json.RawMessage(`{"foo":"bar"}`),
			ProcessConfiguration: mustProcessConfiguration(t, `{
				"startup": {
					"done": ["Done loading"],
					"user_interaction": [],
					"strip_ansi": false
				},
				"stop": {
					"type": "command",
					"value": "stop"
				},
				"configs": [
					{
						"file": "server.properties",
						"parser": "properties",
						"replace": [
							{"match": "server-ip", "replace_with": "0.0.0.0"},
							{"match": "server-port", "replace_with": "{{server.allocations.default.port}}"},
							{"match": "max-players", "replace_with": 20},
							{"match": "enable-query", "replace_with": true}
						],
						"create_file": true
					}
				]
			}`),
		},
	}

	err = store.UpsertServer(context.Background(), definition)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(root, localStoreFilename))
	require.NoError(t, err)

	var persisted struct {
		Servers map[string]struct {
			Configuration struct {
				ProcessConfiguration struct {
					ConfigurationFiles []struct {
						Replace []struct {
							Match       string      `json:"match"`
							ReplaceWith interface{} `json:"replace_with"`
						} `json:"replace"`
					} `json:"configs"`
				} `json:"process_configuration"`
			} `json:"configuration"`
		} `json:"servers"`
	}
	err = json.Unmarshal(data, &persisted)
	require.NoError(t, err)

	replacements := persisted.Servers["server-1"].Configuration.ProcessConfiguration.ConfigurationFiles[0].Replace
	require.Len(t, replacements, 4)
	assert.Equal(t, "0.0.0.0", replacements[0].ReplaceWith)
	assert.Equal(t, "{{server.allocations.default.port}}", replacements[1].ReplaceWith)
	assert.Equal(t, float64(20), replacements[2].ReplaceWith)
	assert.Equal(t, true, replacements[3].ReplaceWith)

	reloadedClient, err := NewLocal(root)
	require.NoError(t, err)

	servers, err := reloadedClient.(*localClient).GetServers(context.Background(), 0)
	require.NoError(t, err)
	require.Len(t, servers, 1)

	var served ProcessConfiguration
	err = json.Unmarshal(servers[0].ProcessConfiguration, &served)
	require.NoError(t, err)
	require.Len(t, served.ConfigurationFiles, 1)
	require.Len(t, served.ConfigurationFiles[0].Replace, 4)

	assert.Equal(t, "0.0.0.0", served.ConfigurationFiles[0].Replace[0].ReplaceWith.String())
	assert.Equal(
		t,
		"{{server.allocations.default.port}}",
		served.ConfigurationFiles[0].Replace[1].ReplaceWith.String(),
	)
	assert.Equal(t, "20", served.ConfigurationFiles[0].Replace[2].ReplaceWith.String())
	assert.Equal(t, "true", served.ConfigurationFiles[0].Replace[3].ReplaceWith.String())
}
