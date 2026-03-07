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
