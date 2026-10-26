//go:build linux || (windows && nativeconsole)

package contract

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
)

func prepareBinaryElvishFieldCompletion(t *testing.T) ([]string, string) {
	t.Helper()
	env, path := prepareBinaryMetadataCompletion(t)
	env = append(env, "JIRA_BASE_URL=", "JIRA_EMAIL=")
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range cachecmd.MetadataResources() {
		if resource.Name == "fields" {
			data := []byte(`{"version":1,"items":[{"id":"customfield_10001","name":"Native field"},{"id":"customfield_10002","name":"Second field"}],"count":2,"pages":1,"complete":true}`)
			if err := store.Set(resource.Key("https://example.com"), data); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "--@complete=cachefield", "--", "--config", path)
			if exit != 0 || stderr != "" || stdout != "customfield_10001\tNative field\ncustomfield_10002\tSecond field\n" {
				t.Fatal("owned Elvish field predictor did not execute", exit, stderr, stdout)
			}
			return env, path
		}
	}
	t.Fatal("missing field completion producer")
	return nil, ""
}
